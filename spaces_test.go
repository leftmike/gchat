package main

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/api/chat/v1"
)

func when(h int) string {
	return time.Date(2026, 11, 20, 9+h, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
}

func TestSpacesCommand(t *testing.T) {
	tr := newTestRun(t)
	tr.f.pageMax = 2

	got := tr.ok("spaces")
	want := lines(
		"AAAAproj001  space  "+when(3)+"  Project X",
		"AAAAteam001  space  "+when(2)+"  Team",
		"AAAAdm00001  dm     "+when(1)+"  (direct message)",
		"AAAAgroup01  group  "+when(0)+"  (group chat)",
		"AAAAproj002  space                    Project Y",
		"5 spaces")
	if got != want {
		t.Errorf("spaces:\ngot\n%s\nwant\n%s", got, want)
	}

	got = tr.ok("spaces", "-type", "group,dm")
	want = lines(
		"AAAAdm00001  dm     "+when(1)+"  (direct message)",
		"AAAAgroup01  group  "+when(0)+"  (group chat)",
		"2 spaces")
	if got != want {
		t.Errorf("spaces -type group,dm:\ngot\n%s\nwant\n%s", got, want)
	}

	got = tr.ok("spaces", "team", "alice@example.com", "spaces/AAAAproj002", "AAAAteam001")
	want = lines(
		"AAAAteam001  space  "+when(2)+"  Team",
		"AAAAdm00001  dm     "+when(1)+"  (direct message)",
		"AAAAproj002  space                    Project Y",
		"3 spaces")
	if got != want {
		t.Errorf("spaces by name:\ngot\n%s\nwant\n%s", got, want)
	}

	tr.fails("unknown space type: chat", "spaces", "-type", "chat")
	tr.fails("-type can't be used", "spaces", "-type", "dm", "team")
	tr.fails("matches more than one space: Project X (AAAAproj001), Project Y (AAAAproj002)",
		"spaces", "proj")
	tr.fails("no such space: nothing", "spaces", "nothing")
	tr.fails("no such space: spaces/AAAAnone001", "spaces", "spaces/AAAAnone001")
	tr.fails("no direct message with bob@example.com", "spaces", "bob@example.com")
	tr.fails("no direct message with bob@elsewhere.org", "spaces", "bob@elsewhere.org")
}

func TestSpaceResolveCalls(t *testing.T) {
	tr := newTestRun(t)
	tr.ok("spaces", "team", "project x")
	want := []string{"GET /v1/spaces"}
	if strings.Join(tr.f.calls, ",") != strings.Join(want, ",") {
		t.Errorf("resolve names: got calls %v, want %v", tr.f.calls, want)
	}

	tr.f.calls = nil
	tr.ok("spaces", "AAAAgroup01", "spaces/AAAAteam001")
	want = []string{"GET /v1/spaces/AAAAgroup01", "GET /v1/spaces/AAAAteam001"}
	if strings.Join(tr.f.calls, ",") != strings.Join(want, ",") {
		t.Errorf("resolve ids: got calls %v, want %v", tr.f.calls, want)
	}

	tr.f.spaces = append(tr.f.spaces, &chat.Space{Name: "spaces/AAAAother01", DisplayName: "Engineering"})
	tr.f.calls = nil
	got := tr.ok("spaces", "engineering")
	if !strings.HasPrefix(got, "AAAAother01  ") {
		t.Errorf("11-letter name: got %q", got)
	}
	want = []string{"GET /v1/spaces/engineering", "GET /v1/spaces"}
	if strings.Join(tr.f.calls, ",") != strings.Join(want, ",") {
		t.Errorf("11-letter name: got calls %v, want %v", tr.f.calls, want)
	}
}

func TestLooksLikeSpaceID(t *testing.T) {
	cases := map[string]bool{
		"AAAANWWLW-g":  true,
		"12hQeYAAAAE":  true,
		"Engineering":  true,
		"Team":         false,
		"Project X 1":  false,
		"AAAANWWLW-gx": false,
	}
	for in, want := range cases {
		if got := looksLikeSpaceID(in); got != want {
			t.Errorf("looksLikeSpaceID(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestMatchSpace(t *testing.T) {
	spaces := []*chat.Space{
		{Name: "spaces/A1", DisplayName: "Eng"},
		{Name: "spaces/A2", DisplayName: "Eng Leads"},
		{Name: "spaces/A3", DisplayName: "eng"},
		{Name: "spaces/A4", DisplayName: "Design"},
		{Name: "spaces/A5"},
	}
	cases := []struct {
		arg, want, err string
	}{
		{"A4", "spaces/A4", ""},
		{"design", "spaces/A4", ""},
		{"DES", "spaces/A4", ""},
		{"eng l", "spaces/A2", ""},
		{"ENG", "", "matches more than one space: Eng (A1), eng (A3)"},
		{"x", "", "no such space: x"},
	}
	for _, c := range cases {
		sp, err := matchSpace(spaces, c.arg)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("matchSpace(%q): got %v, want error %q", c.arg, err, c.err)
			}
		} else if err != nil || sp.Name != c.want {
			t.Errorf("matchSpace(%q): got %v, %v, want %s", c.arg, sp, err, c.want)
		}
	}
}

func TestIsEmail(t *testing.T) {
	cases := map[string]bool{
		"alice@example.com": true,
		"a@b":               true,
		"@example.com":      false,
		"alice@":            false,
		"Team":              false,
		"a b@example.com":   false,
		"spaces/x@y":        false,
	}
	for in, want := range cases {
		if got := isEmail(in); got != want {
			t.Errorf("isEmail(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestSpaceFilter(t *testing.T) {
	got, err := spaceFilter("space, DM")
	if err != nil || got != `spaceType = "SPACE" OR spaceType = "DIRECT_MESSAGE"` {
		t.Errorf("spaceFilter: got %q, %v", got, err)
	}
	if got, err := spaceFilter(""); got != "" || err != nil {
		t.Errorf("spaceFilter empty: got %q, %v", got, err)
	}
}
