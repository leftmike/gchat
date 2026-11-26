package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/chat/v1"
)

var spaceTypes = map[string]string{
	"space":  "SPACE",
	"group":  "GROUP_CHAT",
	"direct": "DIRECT_MESSAGE",
	"dm":     "DIRECT_MESSAGE",
}

var spaceTypeNames = map[string]string{
	"SPACE":          "space",
	"GROUP_CHAT":     "group",
	"DIRECT_MESSAGE": "direct",
}

func spaceTypeName(t string) string {
	if name, ok := spaceTypeNames[t]; ok {
		return name
	}
	return strings.ToLower(t)
}

type spacesOptions struct {
	types string
	max   int
}

func spacesCmd(fs *flag.FlagSet) runFunc {
	var opts spacesOptions
	fs.IntVar(&opts.max, "n", 50, "maximum number of spaces to list; 0 for no limit")
	fs.StringVar(&opts.types, "type", "", "only list spaces of these types: space, group, direct or dm (comma separated)")
	return func(ctx context.Context, cfg *config, args []string) error {
		return spaces(ctx, cfg, opts, args)
	}
}

func spaceFilter(types string) (string, error) {
	if types == "" {
		return "", nil
	}
	var terms []string
	for _, t := range strings.Split(types, ",") {
		st, ok := spaceTypes[strings.ToLower(strings.TrimSpace(t))]
		if !ok {
			return "", fmt.Errorf("unknown space type: %s (want space, group, direct, or dm)", t)
		}
		terms = append(terms, fmt.Sprintf("spaceType = %q", st))
	}
	return strings.Join(terms, " OR "), nil
}

func listSpaces(ctx context.Context, svc *chat.Service, filter string) ([]*chat.Space, error) {
	var spaces []*chat.Space
	var pageToken string
	for {
		resp, err := retry(ctx, func() (*chat.ListSpacesResponse, error) {
			call := svc.Spaces.List().PageSize(maxPageSize).Context(ctx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			if filter != "" {
				call = call.Filter(filter)
			}
			return call.Do()
		})
		if err != nil {
			return nil, err
		}
		spaces = append(spaces, resp.Spaces...)
		if resp.NextPageToken == "" {
			return spaces, nil
		}
		pageToken = resp.NextPageToken
	}
}

func spaceID(name string) string {
	return strings.TrimPrefix(name, "spaces/")
}

func defaultSpaceLabel(sp *chat.Space) string {
	if sp.DisplayName != "" {
		return sp.DisplayName
	}
	switch sp.SpaceType {
	case "DIRECT_MESSAGE":
		return "(direct message)"
	case "GROUP_CHAT":
		return "(group chat)"
	}
	return "(unnamed)"
}

func localTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil || t.Unix() <= 0 {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatSpaceLine(sp *chat.Space, label string) string {
	return fmt.Sprintf("%-11s  %-6s  %-16s  %s", spaceID(sp.Name), spaceTypeName(sp.SpaceType),
		localTime(sp.LastActiveTime), label)
}

func sortSpaces(spaces []*chat.Space) {
	slices.SortStableFunc(spaces, func(a, b *chat.Space) int {
		return cmp.Compare(b.LastActiveTime, a.LastActiveTime)
	})
}

func isEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t/")
}

func looksLikeSpaceID(s string) bool {
	if len(s) != 11 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func getSpace(ctx context.Context, svc *chat.Service, name string) (*chat.Space, error) {
	sp, err := retry(ctx, func() (*chat.Space, error) {
		return svc.Spaces.Get(name).Context(ctx).Do()
	})
	if code := errorCode(err); code == http.StatusNotFound || code == http.StatusForbidden {
		return nil, fmt.Errorf("no such space: %s", name)
	}
	return sp, err
}

func matchSpace(spaces []*chat.Space, arg string) (*chat.Space, error) {
	for _, sp := range spaces {
		if spaceID(sp.Name) == arg {
			return sp, nil
		}
	}

	var exact, prefix []*chat.Space
	lower := strings.ToLower(arg)
	for _, sp := range spaces {
		name := strings.ToLower(sp.DisplayName)
		if name == "" {
			continue
		}
		if name == lower {
			exact = append(exact, sp)
		} else if strings.HasPrefix(name, lower) {
			prefix = append(prefix, sp)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = prefix
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no such space: %s", arg)
	case 1:
		return matches[0], nil
	}
	var names []string
	for _, sp := range matches {
		names = append(names, fmt.Sprintf("%s (%s)", sp.DisplayName, spaceID(sp.Name)))
	}
	return nil, fmt.Errorf("%s matches more than one space: %s", arg, strings.Join(names, ", "))
}

func findDirectMessage(ctx context.Context, svc *chat.Service, email string) (*chat.Space, error) {
	sp, err := retry(ctx, func() (*chat.Space, error) {
		return svc.Spaces.FindDirectMessage().Name("users/" + email).Context(ctx).Do()
	})
	if code := errorCode(err); code == http.StatusNotFound || code == http.StatusBadRequest {
		return nil, fmt.Errorf("no direct message with %s", email)
	}
	return sp, err
}

type spaceResolver struct {
	svc    *chat.Service
	spaces []*chat.Space
}

func (r *spaceResolver) resolve(ctx context.Context, arg string) (*chat.Space, error) {
	if arg == "" {
		return nil, errors.New("empty space name")
	}
	if isEmail(arg) {
		return findDirectMessage(ctx, r.svc, arg)
	}
	if strings.HasPrefix(arg, "spaces/") {
		return getSpace(ctx, r.svc, arg)
	}
	if looksLikeSpaceID(arg) {
		if sp, err := getSpace(ctx, r.svc, "spaces/"+arg); err == nil {
			return sp, nil
		}
	}
	if r.spaces == nil {
		spaces, err := listSpaces(ctx, r.svc, "")
		if err != nil {
			return nil, err
		}
		r.spaces = spaces
	}
	return matchSpace(r.spaces, arg)
}

func resolveSpaces(ctx context.Context, svc *chat.Service, args []string) ([]*chat.Space, error) {
	r := &spaceResolver{svc: svc}
	var spaces []*chat.Space
	for _, arg := range args {
		sp, err := r.resolve(ctx, arg)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(spaces, func(s *chat.Space) bool { return s.Name == sp.Name }) {
			spaces = append(spaces, sp)
		}
	}
	return spaces, nil
}

func spaces(ctx context.Context, cfg *config, opts spacesOptions, args []string) error {
	if opts.max < 0 {
		return errors.New("-n must be 0 or more")
	}
	filter, err := spaceFilter(opts.types)
	if err != nil {
		return err
	}
	if filter != "" && len(args) > 0 {
		return errors.New("-type can't be used with spaces given by name")
	}
	svc, err := cfg.chatService(ctx)
	if err != nil {
		return err
	}

	var list []*chat.Space
	more := false
	if len(args) > 0 {
		list, err = resolveSpaces(ctx, svc, args)
	} else {
		list, err = listSpaces(ctx, svc, filter)
		sortSpaces(list)
		if opts.max > 0 && len(list) > opts.max {
			list, more = list[:opts.max], true
		}
	}
	if err != nil {
		return err
	}

	c, err := cfg.loadCache(ctx)
	if err != nil {
		return err
	}
	defer cfg.saveCache()
	fetchErr := fetchMembers(ctx, cfg, svc, c, list)

	for _, sp := range list {
		fmt.Fprintln(cfg.out, formatSpaceLine(sp, c.spaceLabel(sp)))
	}
	fmt.Fprintf(cfg.out, "%d spaces", len(list))
	if more {
		fmt.Fprint(cfg.out, " (more; use -n to see more)")
	}
	fmt.Fprintln(cfg.out)
	return fetchErr
}
