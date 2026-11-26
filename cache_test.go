package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/chat/v1"
)

func memberCalls(f *fakeChat) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasSuffix(c, "/members") {
			n++
		}
	}
	return n
}

func TestSpacesCache(t *testing.T) {
	tr := newTestRun(t)
	first := tr.ok("spaces")
	if n := memberCalls(tr.f); n != 2 {
		t.Errorf("first run: %d member calls, want 2: %v", n, tr.f.calls)
	}

	tr.f.calls = nil
	if got := tr.ok("spaces"); got != first {
		t.Errorf("cached run:\ngot\n%s\nwant\n%s", got, first)
	}
	if n := memberCalls(tr.f); n != 0 || strings.Contains(strings.Join(tr.f.calls, ","), "userinfo") {
		t.Errorf("cached run: calls %v", tr.f.calls)
	}

	c, err := readCache(tr.cache)
	if err != nil {
		t.Fatal(err)
	}
	c.Spaces["spaces/AAAAgroup01"].Fetched = time.Now().Add(-groupMembersTTL - time.Hour)
	c.Spaces["spaces/AAAAdm00001"].Fetched = time.Now().Add(-365 * 24 * time.Hour)
	c.changed = true
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	tr.f.members["spaces/AAAAgroup01"] = []string{"users/1", "users/3"}
	tr.f.calls = nil
	got := tr.ok("spaces", "-type", "group,dm")
	want := lines(
		"AAAAdm00001  direct  "+when(1)+"  Alice Smith",
		"AAAAgroup01  group   "+when(0)+"  Bob Jones",
		"2 spaces")
	if got != want {
		t.Errorf("stale group:\ngot\n%s\nwant\n%s", got, want)
	}
	if n := memberCalls(tr.f); n != 1 {
		t.Errorf("stale group: %d member calls, want 1: %v", n, tr.f.calls)
	}

	fi, err := os.Stat(tr.cache)
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("cache file: %v, %v", fi, err)
	}
}

func TestSpacesLimit(t *testing.T) {
	tr := newTestRun(t)
	got := tr.ok("spaces", "-n", "2")
	want := lines(
		"AAAAproj001  space   "+when(3)+"  Project X",
		"AAAAteam001  space   "+when(2)+"  Team",
		"2 spaces (more; use -n to see more)")
	if got != want {
		t.Errorf("spaces -n 2:\ngot\n%s\nwant\n%s", got, want)
	}
	if n := memberCalls(tr.f); n != 0 {
		t.Errorf("spaces -n 2: %d member calls, want 0", n)
	}
	if got := tr.ok("spaces", "-n", "0"); !strings.HasSuffix(got, "\n5 spaces\n") {
		t.Errorf("spaces -n 0: got %q", got)
	}
	tr.fails("-n must be 0 or more", "spaces", "-n", "-1")
}

func TestSpacesMemberErrors(t *testing.T) {
	tr := newTestRun(t)
	tr.f.membersFail = map[string]bool{"spaces/AAAAgroup01": true}
	out, errOut, err := tr.run("spaces", "-type", "group,dm")
	want := lines(
		"AAAAdm00001  direct  "+when(1)+"  Alice Smith",
		"AAAAgroup01  group   "+when(0)+"  (group chat)",
		"2 spaces")
	if out != want || !strings.HasPrefix(errOut, "skipping: spaces/AAAAgroup01: ") ||
		err == nil || err.Error() != "1 of 2 failed" {
		t.Errorf("spaces: got %q, %q, %v", out, errOut, err)
	}

	delete(tr.f.membersFail, "spaces/AAAAgroup01")
	tr.f.calls = nil
	if got := tr.ok("spaces", "-type", "group"); !strings.Contains(got, "Alice Smith, Bob Jones") {
		t.Errorf("retried group: got %q", got)
	}
	if n := memberCalls(tr.f); n != 1 {
		t.Errorf("retried group: %d member calls, want 1", n)
	}
}

func TestCacheCorrupt(t *testing.T) {
	tr := newTestRun(t)
	if err := os.WriteFile(tr.cache, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := tr.run("spaces", "-type", "dm")
	if err != nil || !strings.Contains(out, "Alice Smith") ||
		!strings.HasPrefix(errOut, "gchat spaces: warning: "+tr.cache+": ") {
		t.Errorf("corrupt cache: got %q, %q, %v", out, errOut, err)
	}
	if _, err := readCache(tr.cache); err != nil {
		t.Errorf("cache not rewritten: %v", err)
	}

	tr.cache = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(tr.cache, nil, 0600); err != nil {
		t.Fatal(err)
	}
	tr.cache = filepath.Join(tr.cache, "cache.json")
	out, errOut, err = tr.run("spaces", "-type", "dm")
	if err != nil || !strings.Contains(out, "Alice Smith") ||
		!strings.Contains(errOut, "warning: saving cache: ") {
		t.Errorf("unwritable cache: got %q, %q, %v", out, errOut, err)
	}
}

func TestSpaceLabel(t *testing.T) {
	c := &cache{MeEmail: "Me@Example.com"}
	c.init()
	c.Users = map[string]*cachedUser{
		"users/1": {Name: "Me Myself", Email: "me@example.com"},
		"users/2": {Name: "Alice Smith", Email: "alice@example.com"},
		"users/3": {Email: "bob@example.com"},
		"users/8": {Name: "Helper", Bot: true},
		"users/9": {Bot: true},
	}
	c.Spaces = map[string]*cachedSpace{
		"spaces/dm1": {Members: []string{"users/1", "users/2", "users/9"}},
		"spaces/dm2": {Members: []string{"users/1", "users/8"}},
		"spaces/dm3": {Members: []string{"users/1", "users/9"}},
		"spaces/g1":  {Members: []string{"users/1", "users/3", "users/7"}},
	}
	cases := []struct {
		sp   *chat.Space
		want string
	}{
		{&chat.Space{Name: "spaces/dm1", SpaceType: "DIRECT_MESSAGE"}, "Alice Smith"},
		{&chat.Space{Name: "spaces/dm2", SpaceType: "DIRECT_MESSAGE"}, "Helper"},
		{&chat.Space{Name: "spaces/dm3", SpaceType: "DIRECT_MESSAGE"}, "(direct message)"},
		{&chat.Space{Name: "spaces/g1", SpaceType: "GROUP_CHAT"}, "bob@example.com, users/7"},
		{&chat.Space{Name: "spaces/g2", SpaceType: "GROUP_CHAT"}, "(group chat)"},
		{&chat.Space{Name: "spaces/dm1", DisplayName: "Named", SpaceType: "SPACE"}, "Named"},
	}
	for _, tc := range cases {
		if got := c.spaceLabel(tc.sp); got != tc.want {
			t.Errorf("spaceLabel(%s): got %q, want %q", tc.sp.Name, got, tc.want)
		}
	}
}

func TestAddUser(t *testing.T) {
	c := &cache{}
	c.init()
	c.addUser(&chat.User{Name: "users/2", DisplayName: "Alice", Email: "a@example.com", Type: "HUMAN"})
	c.changed = false
	c.addUser(&chat.User{Name: "users/2", Type: "HUMAN"})
	c.addUser(&chat.User{Name: "users/2", DisplayName: "Alice", Email: "a@example.com", Type: "HUMAN"})
	if c.changed || c.Users["users/2"].Name != "Alice" {
		t.Errorf("addUser: unnamed or same user changed the cache: %+v", c.Users["users/2"])
	}
	c.addUser(&chat.User{Name: "users/2", DisplayName: "Alice B", Email: "a@example.com", Type: "HUMAN"})
	if !c.changed || c.Users["users/2"].Name != "Alice B" {
		t.Errorf("addUser: rename not saved: %+v", c.Users["users/2"])
	}
}
