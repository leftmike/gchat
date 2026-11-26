package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"slices"
	"time"

	"google.golang.org/api/chat/v1"
)

type listOptions struct {
	max    int
	since  string
	thread string
	grep   string
}

func listCmd(fs *flag.FlagSet) runFunc {
	var opts listOptions
	fs.IntVar(&opts.max, "n", 50, "maximum number of messages to list; 0 for no limit")
	fs.StringVar(&opts.since, "since", "", "only messages after this time: 30m, 12h, 2d, 1w, or a date")
	fs.StringVar(&opts.thread, "thread", "", "only messages in the thread of this message")
	fs.StringVar(&opts.grep, "grep", "", "only messages whose text matches this regular expression")
	return func(ctx context.Context, cfg *config, args []string) error {
		return list(ctx, cfg, opts, args)
	}
}

const replyMark = "↳ "

func formatListLine(c *cache, m *chat.Message) string {
	mark := ""
	if m.ThreadReply {
		mark = replyMark
	}
	return fmt.Sprintf("%s  %s  %s  %s%s", shortID(m.Name), localTime(m.CreateTime),
		fit(c.senderName(m.Sender), 20), mark, summary(m))
}

func list(ctx context.Context, cfg *config, opts listOptions, args []string) error {
	if opts.max < 0 {
		return errors.New("-n must be 0 or more")
	}
	if len(args) > 1 || (len(args) == 0 && opts.thread == "") {
		return usageError("[-n <max>] [-since <time>] [-thread <id>] [-grep <regexp>] <space>")
	}
	var since time.Time
	if opts.since != "" {
		var err error
		if since, err = parseSince(opts.since, time.Now()); err != nil {
			return err
		}
	}
	var keep func(*chat.Message) bool
	if opts.grep != "" {
		re, err := regexp.Compile(opts.grep)
		if err != nil {
			return fmt.Errorf("-grep: %w", err)
		}
		keep = func(m *chat.Message) bool { return re.MatchString(m.Text) }
	}

	svc, err := cfg.chatService(ctx)
	if err != nil {
		return err
	}

	var space, thread string
	if opts.thread != "" {
		name, err := messageName(opts.thread)
		if err != nil {
			return err
		}
		m, err := getMessage(ctx, svc, name)
		if err != nil {
			return err
		}
		if m.Thread == nil || m.Thread.Name == "" {
			return fmt.Errorf("%s isn't in a thread", opts.thread)
		}
		space, thread = spaceOf(m.Name), m.Thread.Name
	}
	if len(args) == 1 {
		sp, err := (&spaceResolver{svc: svc}).resolve(ctx, args[0])
		if err != nil {
			return err
		}
		if space != "" && sp.Name != space {
			return fmt.Errorf("%s is in a different space", opts.thread)
		}
		space = sp.Name
	}

	msgs, more, err := listMessages(ctx, svc, space, messageFilter(since, thread), opts.max, keep)
	if err != nil {
		return err
	}
	c, err := cfg.loadCache(ctx)
	if err != nil {
		return err
	}
	defer cfg.saveCache()

	for _, m := range slices.Backward(msgs) {
		c.addUser(m.Sender)
		fmt.Fprintln(cfg.out, formatListLine(c, m))
	}
	fmt.Fprintf(cfg.out, "%d messages", len(msgs))
	if more && keep != nil {
		fmt.Fprint(cfg.out, " (more may match; use -n to see more)")
	} else if more {
		fmt.Fprint(cfg.out, " (more; use -n to see more)")
	}
	fmt.Fprintln(cfg.out)
	return nil
}
