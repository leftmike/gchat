package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/api/chat/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestMain(m *testing.M) {
	limiter = rate.NewLimiter(rate.Inf, 0)
	writeRate = rate.Inf
	retryDelay = time.Millisecond
	os.Exit(m.Run())
}

func newTestService(t *testing.T, h http.HandlerFunc) (*chat.Service, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	svc, err := chat.NewService(context.Background(), option.WithEndpoint(srv.URL+"/"),
		option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return svc, srv.Client()
}

func TestRetry(t *testing.T) {
	var calls atomic.Int32
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, `{"error":{"code":503,"message":"unavailable"}}`, http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"name":"spaces/a","displayName":"A"}`)
	})

	var log strings.Builder
	logOut, verbose = &log, true
	t.Cleanup(func() { logOut, verbose = os.Stderr, false })

	sp, err := retry(t.Context(), func() (*chat.Space, error) {
		return svc.Spaces.Get("spaces/a").Context(t.Context()).Do()
	})
	if err != nil {
		t.Fatal(err)
	}
	if sp.DisplayName != "A" || calls.Load() != 3 {
		t.Errorf("retry: got %+v after %d calls", sp, calls.Load())
	}
	if n := strings.Count(log.String(), "retrying in "); n != 2 {
		t.Errorf("retry: logged %d retries: %q", n, log.String())
	}

	writes := 0
	_, err = retryWrite(t.Context(), "spaces/a/messages/m", func() (*chat.Space, error) {
		writes++
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	})
	if err == nil || writes != 1 {
		t.Errorf("retryWrite 404: got %v after %d calls", err, writes)
	}
}

func TestRetryGivesUp(t *testing.T) {
	calls := 0
	_, err := retry(t.Context(), func() (int, error) {
		calls++
		return 0, &googleapi.Error{Code: http.StatusTooManyRequests}
	})
	if err == nil || calls != maxRetries {
		t.Errorf("retry: got %v after %d calls, want %d", err, calls, maxRetries)
	}
}

func TestSpaceOf(t *testing.T) {
	cases := map[string]string{
		"spaces/AAAA":                            "spaces/AAAA",
		"spaces/AAAA/messages/BBBB.CCCC":         "spaces/AAAA",
		"spaces/AAAA/messages/BBBB/reactions/xy": "spaces/AAAA",
		"users/123":                              "users/123",
	}
	for in, want := range cases {
		if got := spaceOf(in); got != want {
			t.Errorf("spaceOf(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestWriteLimiter(t *testing.T) {
	a := writeLimiter("spaces/a/messages/m1")
	if writeLimiter("spaces/a") != a || writeLimiter("spaces/a/messages/m2") != a {
		t.Error("writeLimiter: same space got different limiters")
	}
	if writeLimiter("spaces/b") == a {
		t.Error("writeLimiter: different spaces share a limiter")
	}

	saved := writeRate
	writeRate = 1
	t.Cleanup(func() { writeRate = saved })
	lim := writeLimiter("spaces/paced")
	if lim.Limit() != 1 || lim.Burst() != 1 {
		t.Errorf("writeLimiter: got limit %v burst %d", lim.Limit(), lim.Burst())
	}
	if !lim.Allow() || lim.Allow() {
		t.Error("writeLimiter: want one write, then wait")
	}
}

func TestEach(t *testing.T) {
	var names, want []string
	bad := map[string]bool{}
	for i := range 250 {
		name := fmt.Sprintf("spaces/s/messages/m%d", i)
		names = append(names, name)
		if i%50 == 7 {
			bad[name] = true
		} else {
			want = append(want, name)
		}
	}

	var skipped strings.Builder
	var got []string
	err := each(context.Background(), &skipped, names,
		func(ctx context.Context, name string) (string, error) {
			if bad[name] {
				return "", errors.New("fail")
			}
			return name, nil
		},
		func(name string, v string) error {
			got = append(got, v)
			return nil
		})
	if err == nil || err.Error() != "5 of 250 failed" {
		t.Errorf("each: got err %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("each: got %d results, want %d in order", len(got), len(want))
	}
	var wantSkipped string
	for _, i := range []int{7, 57, 107, 157, 207} {
		wantSkipped += fmt.Sprintf("skipping: spaces/s/messages/m%d: fail\n", i)
	}
	if skipped.String() != wantSkipped {
		t.Errorf("each: skipped %q, want %q", skipped.String(), wantSkipped)
	}

	stop := errors.New("stop")
	n := 0
	err = each(context.Background(), io.Discard, names,
		func(ctx context.Context, name string) (string, error) { return name, nil },
		func(name string, v string) error {
			n++
			if n == 3 {
				return stop
			}
			return nil
		})
	if !errors.Is(err, stop) || n != 3 {
		t.Errorf("each stop: got err %v after %d", err, n)
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&googleapi.Error{Code: 429}, true},
		{&googleapi.Error{Code: 503}, true},
		{fmt.Errorf("wrapped: %w", &googleapi.Error{Code: 500}), true},
		{&googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}}}, true},
		{&googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}}, false},
		{&googleapi.Error{Code: 404}, false},
		{&googleapi.Error{Code: 400}, false},
		{errors.New("network"), false},
	}
	for _, c := range cases {
		if got := retryable(c.err); got != c.want {
			t.Errorf("retryable(%v): got %v, want %v", c.err, got, c.want)
		}
	}
}
