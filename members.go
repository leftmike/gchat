package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/chat/v1"
)

const maxLabelNames = 3

func listMembers(ctx context.Context, svc *chat.Service, space string) ([]*chat.Membership, error) {
	var members []*chat.Membership
	var pageToken string
	for {
		resp, err := retry(ctx, func() (*chat.ListMembershipsResponse, error) {
			call := svc.Spaces.Members.List(space).PageSize(maxPageSize).Context(ctx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			return call.Do()
		})
		if err != nil {
			return nil, err
		}
		members = append(members, resp.Memberships...)
		if resp.NextPageToken == "" {
			return members, nil
		}
		pageToken = resp.NextPageToken
	}
}

func fetchMembers(ctx context.Context, cfg *config, svc *chat.Service, c *cache, spaces []*chat.Space) error {
	now := time.Now()
	var names []string
	for _, sp := range spaces {
		if c.needsMembers(sp, now) {
			names = append(names, sp.Name)
		}
	}
	return each(ctx, cfg.errOut, names,
		func(ctx context.Context, name string) ([]*chat.Membership, error) {
			return listMembers(ctx, svc, name)
		},
		func(name string, ms []*chat.Membership) error {
			c.setMembers(name, ms, now)
			return nil
		})
}

func (c *cache) userName(id string) string {
	if u, ok := c.Users[id]; ok {
		if u.Name != "" {
			return u.Name
		}
		if u.Email != "" {
			return u.Email
		}
	}
	return id
}

func (c *cache) spaceLabel(sp *chat.Space) string {
	if sp.DisplayName != "" {
		return sp.DisplayName
	}
	cs, ok := c.Spaces[sp.Name]
	if !ok {
		return defaultSpaceLabel(sp)
	}
	var humans, bots []string
	for _, id := range cs.Members {
		u := c.Users[id]
		if u != nil && c.MeEmail != "" && strings.EqualFold(u.Email, c.MeEmail) {
			continue
		}
		if u != nil && u.Bot {
			if u.Name != "" {
				bots = append(bots, u.Name)
			}
			continue
		}
		humans = append(humans, c.userName(id))
	}
	names := humans
	if len(names) == 0 {
		names = bots
	}
	if len(names) == 0 {
		return defaultSpaceLabel(sp)
	}
	if len(names) > maxLabelNames {
		return fmt.Sprintf("%s +%d", strings.Join(names[:maxLabelNames], ", "), len(names)-maxLabelNames)
	}
	return strings.Join(names, ", ")
}

var roleNames = map[string]string{
	"ROLE_MANAGER":           "owner",
	"ROLE_ASSISTANT_MANAGER": "manager",
	"ROLE_MEMBER":            "member",
}

var roleOrder = map[string]int{"ROLE_MANAGER": 0, "ROLE_ASSISTANT_MANAGER": 1, "ROLE_MEMBER": 2}

func roleName(r string) string {
	if name, ok := roleNames[r]; ok {
		return name
	}
	return "-"
}

func (c *cache) memberName(m *chat.Membership) string {
	if m.Member != nil {
		return c.senderName(m.Member)
	}
	if m.GroupMember != nil {
		return m.GroupMember.Name
	}
	return ""
}

func memberDetail(m *chat.Membership) string {
	switch {
	case m.GroupMember != nil:
		return "(google group)"
	case m.Member == nil:
		return ""
	case m.Member.Type == "BOT":
		return "(app)"
	}
	return m.Member.Email
}

func members(ctx context.Context, cfg *config, args []string) error {
	if len(args) != 1 {
		return usageError("<space>")
	}
	svc, err := cfg.chatService(ctx)
	if err != nil {
		return err
	}
	sp, err := (&spaceResolver{svc: svc}).resolve(ctx, args[0])
	if err != nil {
		return err
	}
	ms, err := listMembers(ctx, svc, sp.Name)
	if err != nil {
		return err
	}
	c, err := cfg.loadCache(ctx)
	if err != nil {
		return err
	}
	defer cfg.saveCache()
	c.setMembers(sp.Name, ms, time.Now())

	slices.SortStableFunc(ms, func(a, b *chat.Membership) int {
		ra, oka := roleOrder[a.Role]
		rb, okb := roleOrder[b.Role]
		if !oka {
			ra = len(roleOrder)
		}
		if !okb {
			rb = len(roleOrder)
		}
		return cmp.Or(cmp.Compare(ra, rb),
			cmp.Compare(strings.ToLower(c.memberName(a)), strings.ToLower(c.memberName(b))))
	})
	for _, m := range ms {
		fmt.Fprintln(cfg.out, strings.TrimRight(fmt.Sprintf("%-7s  %-24s  %s", roleName(m.Role),
			c.memberName(m), memberDetail(m)), " "))
	}
	fmt.Fprintf(cfg.out, "%d members\n", len(ms))
	return nil
}
