package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/chat/v1"
)

func messageName(arg string) (string, error) {
	parts := strings.Split(arg, "/")
	switch {
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "messages" && parts[1] != "" && parts[3] != "":
		return arg, nil
	case len(parts) == 2 && parts[0] != "" && parts[1] != "":
		return "spaces/" + parts[0] + "/messages/" + parts[1], nil
	}
	return "", fmt.Errorf("bad message id: %s (want <space>/<message>)", arg)
}

func messageNames(args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		name, err := messageName(arg)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func shortID(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) == 4 {
		return parts[1] + "/" + parts[3]
	}
	return name
}

func getMessage(ctx context.Context, svc *chat.Service, name string) (*chat.Message, error) {
	m, err := retry(ctx, func() (*chat.Message, error) {
		return svc.Spaces.Messages.Get(name).Context(ctx).Do()
	})
	if notFound(err) {
		return nil, fmt.Errorf("no such message: %s", shortID(name))
	}
	return m, err
}

func parseSince(s string, now time.Time) (time.Time, error) {
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour,
		'w': 7 * 24 * time.Hour}
	if len(s) > 1 {
		if unit, ok := units[s[len(s)-1]]; ok {
			if n, err := strconv.Atoi(s[:len(s)-1]); err == nil && n >= 0 {
				return now.Add(-time.Duration(n) * unit), nil
			}
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("bad time: %s (want 30m, 12h, 2d, 1w, 2006-01-02, or 2006-01-02 15:04)", s)
}

func messageFilter(since time.Time, thread string) string {
	var terms []string
	if !since.IsZero() {
		terms = append(terms, fmt.Sprintf("createTime > %q", since.UTC().Format(time.RFC3339)))
	}
	if thread != "" {
		terms = append(terms, "thread.name = "+thread)
	}
	return strings.Join(terms, " AND ")
}

func listMessages(ctx context.Context, svc *chat.Service, space, filter string, max int,
	keep func(*chat.Message) bool) ([]*chat.Message, bool, error) {

	var msgs []*chat.Message
	var pageToken string
	for {
		size := maxPageSize
		if max > 0 && keep == nil {
			size = min(size, max-len(msgs))
		}
		resp, err := retry(ctx, func() (*chat.ListMessagesResponse, error) {
			call := svc.Spaces.Messages.List(space).OrderBy("createTime desc").PageSize(int64(size)).
				Context(ctx)
			if filter != "" {
				call = call.Filter(filter)
			}
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			return call.Do()
		})
		if err != nil {
			return nil, false, err
		}
		for _, m := range resp.Messages {
			if keep != nil && !keep(m) {
				continue
			}
			if max > 0 && len(msgs) == max {
				return msgs, true, nil
			}
			msgs = append(msgs, m)
		}
		if resp.NextPageToken == "" {
			return msgs, false, nil
		}
		if max > 0 && len(msgs) == max {
			return msgs, true, nil
		}
		pageToken = resp.NextPageToken
	}
}

func (c *cache) senderName(u *chat.User) string {
	if u == nil {
		return ""
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if u.Email != "" {
		return u.Email
	}
	return c.userName(u.Name)
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = oneLine(line); line != "" {
			return line
		}
	}
	return ""
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func fit(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}

func summary(m *chat.Message) string {
	if m.DeletionMetadata != nil {
		return "(deleted)"
	}
	if s := firstLine(m.Text); s != "" {
		return s
	}
	if len(m.Attachment) > 0 {
		return "(attachment: " + m.Attachment[0].ContentName + ")"
	}
	if len(m.CardsV2) > 0 || len(m.Cards) > 0 {
		return "(card)"
	}
	return ""
}
