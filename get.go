package main

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/chat/v1"
)

func reactionSummary(rs []*chat.EmojiReactionSummary) string {
	var parts []string
	for _, r := range rs {
		if r.Emoji == nil {
			continue
		}
		e := r.Emoji.Unicode
		if e == "" && r.Emoji.CustomEmoji != nil {
			e = ":" + r.Emoji.CustomEmoji.EmojiName + ":"
		}
		parts = append(parts, fmt.Sprintf("%s %d", e, r.ReactionCount))
	}
	return strings.Join(parts, ", ")
}

func formatMessage(c *cache, m *chat.Message) string {
	var b strings.Builder
	field := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%-12s%s\n", name+":", value)
		}
	}
	field("id", shortID(m.Name))
	from := c.senderName(m.Sender)
	if m.Sender != nil && m.Sender.Email != "" && from != m.Sender.Email {
		from += " <" + m.Sender.Email + ">"
	}
	field("from", from)
	field("date", localTime(m.CreateTime))
	if m.LastUpdateTime != "" {
		field("edited", localTime(m.LastUpdateTime))
	}
	if m.ThreadReply && m.Thread != nil {
		field("thread", shortID(m.Thread.Name))
	}
	if m.DeletionMetadata != nil {
		field("deleted", localTime(m.DeleteTime))
	}
	field("reactions", reactionSummary(m.EmojiReactionSummaries))
	for _, a := range m.Attachment {
		field("attachment", fmt.Sprintf("%s (%s)", a.ContentName, a.ContentType))
	}
	if len(m.CardsV2) > 0 || len(m.Cards) > 0 {
		field("cards", fmt.Sprint(len(m.CardsV2)+len(m.Cards)))
	}
	if text := strings.TrimRight(m.Text, "\n"); text != "" {
		fmt.Fprintf(&b, "\n%s\n", text)
	}
	return b.String()
}

func get(ctx context.Context, cfg *config, args []string) error {
	if len(args) == 0 {
		return usageError("<id>...")
	}
	names, err := messageNames(args)
	if err != nil {
		return err
	}
	svc, err := cfg.chatService(ctx)
	if err != nil {
		return err
	}
	c, err := cfg.loadCache(ctx)
	if err != nil {
		return err
	}
	defer cfg.saveCache()

	first := true
	return each(ctx, cfg.errOut, names,
		func(ctx context.Context, name string) (*chat.Message, error) {
			return getMessage(ctx, svc, name)
		},
		func(name string, m *chat.Message) error {
			if !first {
				fmt.Fprintln(cfg.out)
			}
			first = false
			c.addUser(m.Sender)
			fmt.Fprint(cfg.out, formatMessage(c, m))
			return nil
		})
}
