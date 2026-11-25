package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"google.golang.org/api/googleapi"
)

const (
	fetchWorkers = 10
	fetchChunk   = 100
	maxRetries   = 5
	maxPageSize  = 1000
)

var (
	logOut     io.Writer = os.Stderr
	retryDelay           = time.Second
	limiter              = rate.NewLimiter(10, 10)
	writeRate            = rate.Limit(1)

	writeMu       sync.Mutex
	writeLimiters = map[string]*rate.Limiter{}
)

func retryable(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	switch gerr.Code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden:
		for _, e := range gerr.Errors {
			if e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded" {
				return true
			}
		}
	}
	return false
}

func errorCode(err error) int {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code
	}
	return 0
}

func notFound(err error) bool {
	return errorCode(err) == http.StatusNotFound
}

func spaceOf(name string) string {
	parts := strings.SplitN(name, "/", 3)
	if len(parts) < 2 || parts[0] != "spaces" {
		return name
	}
	return parts[0] + "/" + parts[1]
}

func writeLimiter(name string) *rate.Limiter {
	space := spaceOf(name)
	writeMu.Lock()
	defer writeMu.Unlock()
	lim, ok := writeLimiters[space]
	if !ok {
		lim = rate.NewLimiter(writeRate, 1)
		writeLimiters[space] = lim
	}
	return lim
}

func retryWith[T any](ctx context.Context, lim *rate.Limiter, call func() (T, error)) (T, error) {
	delay := retryDelay
	for attempt := 1; ; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			var zero T
			return zero, err
		}
		if lim != nil {
			if err := lim.Wait(ctx); err != nil {
				var zero T
				return zero, err
			}
		}
		v, err := call()
		if err == nil || attempt == maxRetries || !retryable(err) {
			return v, err
		}
		if verbose {
			fmt.Fprintf(logOut, "retrying in %s: %s\n", delay, err)
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(delay + rand.N(delay/2+1)):
		}
		delay *= 2
	}
}

func retry[T any](ctx context.Context, call func() (T, error)) (T, error) {
	return retryWith(ctx, nil, call)
}

func retryWrite[T any](ctx context.Context, name string, call func() (T, error)) (T, error) {
	return retryWith(ctx, writeLimiter(name), call)
}

func each[T any](ctx context.Context, errOut io.Writer, names []string,
	fetch func(ctx context.Context, name string) (T, error), fn func(name string, v T) error) error {

	failed := 0
	for start := 0; start < len(names); start += fetchChunk {
		chunk := names[start:min(start+fetchChunk, len(names))]
		vals := make([]T, len(chunk))
		errs := make([]error, len(chunk))

		var g errgroup.Group
		g.SetLimit(fetchWorkers)
		for i, name := range chunk {
			g.Go(func() error {
				vals[i], errs[i] = fetch(ctx, name)
				return nil
			})
		}
		g.Wait()
		if err := ctx.Err(); err != nil {
			return err
		}

		for i, name := range chunk {
			if errs[i] != nil {
				fmt.Fprintf(errOut, "skipping: %s: %s\n", name, errs[i])
				failed += 1
				continue
			}
			if err := fn(name, vals[i]); err != nil {
				return err
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d failed", failed, len(names))
	}
	return nil
}
