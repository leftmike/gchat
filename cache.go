package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/api/chat/v1"
)

const groupMembersTTL = 7 * 24 * time.Hour

type cachedUser struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Bot   bool   `json:"bot,omitempty"`
}

type cachedSpace struct {
	Members []string  `json:"members"`
	Fetched time.Time `json:"fetched"`
}

type cache struct {
	MeEmail string                  `json:"meEmail,omitempty"`
	Users   map[string]*cachedUser  `json:"users,omitempty"`
	Spaces  map[string]*cachedSpace `json:"spaces,omitempty"`

	path    string
	changed bool
}

func readCache(path string) (*cache, error) {
	c := &cache{path: path}
	buf, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		c.init()
		return c, err
	}
	if err == nil {
		if err := json.Unmarshal(buf, c); err != nil {
			c = &cache{path: path, changed: true}
			err = fmt.Errorf("%s: %w; starting over", path, err)
			c.init()
			return c, err
		}
	}
	c.init()
	return c, nil
}

func (c *cache) init() {
	if c.Users == nil {
		c.Users = map[string]*cachedUser{}
	}
	if c.Spaces == nil {
		c.Spaces = map[string]*cachedSpace{}
	}
}

func (c *cache) save() error {
	if !c.changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return err
	}
	buf, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	c.changed = false
	return nil
}

func (c *cache) addUser(u *chat.User) {
	if u == nil || u.Name == "" {
		return
	}
	cu := &cachedUser{Name: u.DisplayName, Email: u.Email, Bot: u.Type == "BOT"}
	old, ok := c.Users[u.Name]
	if ok && (cu.Name == "" || *old == *cu) {
		return
	}
	c.Users[u.Name] = cu
	c.changed = true
}

func (c *cache) setMembers(space string, ms []*chat.Membership, now time.Time) {
	var ids []string
	for _, m := range ms {
		if m.Member == nil {
			continue
		}
		c.addUser(m.Member)
		ids = append(ids, m.Member.Name)
	}
	c.Spaces[space] = &cachedSpace{Members: ids, Fetched: now}
	c.changed = true
}

func (c *cache) needsMembers(sp *chat.Space, now time.Time) bool {
	switch sp.SpaceType {
	case "DIRECT_MESSAGE":
		_, ok := c.Spaces[sp.Name]
		return !ok
	case "GROUP_CHAT":
		cs, ok := c.Spaces[sp.Name]
		return !ok || now.Sub(cs.Fetched) > groupMembersTTL
	}
	return false
}

func (cfg *config) loadCache(ctx context.Context) (*cache, error) {
	if cfg.cache != nil {
		return cfg.cache, nil
	}
	c, err := readCache(cfg.Cache)
	if err != nil {
		warn(cfg, err)
	}
	if c.MeEmail == "" {
		client, err := cfg.httpClient(ctx)
		if err != nil {
			return nil, err
		}
		c.MeEmail, err = userEmail(ctx, client)
		if err != nil {
			return nil, err
		}
		c.changed = true
	}
	cfg.cache = c
	return c, nil
}

func (cfg *config) saveCache() {
	if cfg.cache == nil {
		return
	}
	if err := cfg.cache.save(); err != nil {
		warn(cfg, fmt.Errorf("saving cache: %w", err))
	}
}
