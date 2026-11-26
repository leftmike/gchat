package main

import (
	"context"
	"fmt"
	"slices"
)

func thread(ctx context.Context, cfg *config, args []string) error {
	if len(args) != 1 {
		return usageError("<id>")
	}
	name, err := messageName(args[0])
	if err != nil {
		return err
	}
	svc, err := cfg.chatService(ctx)
	if err != nil {
		return err
	}
	m, err := getMessage(ctx, svc, name)
	if err != nil {
		return err
	}
	if m.Thread == nil || m.Thread.Name == "" {
		return fmt.Errorf("%s isn't in a thread", args[0])
	}
	msgs, _, err := listMessages(ctx, svc, spaceOf(m.Name), messageFilter(zeroTime, m.Thread.Name), 0, nil)
	if err != nil {
		return err
	}
	c, err := cfg.loadCache(ctx)
	if err != nil {
		return err
	}
	defer cfg.saveCache()

	for i, m := range slices.Backward(msgs) {
		if i != len(msgs)-1 {
			fmt.Fprintln(cfg.out)
		}
		c.addUser(m.Sender)
		fmt.Fprint(cfg.out, formatMessage(c, m))
	}
	return nil
}
