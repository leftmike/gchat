package main

import (
	"strings"
	"testing"
	"time"
)

func at(min int) string {
	return time.Date(2026, 11, 20, 9, min, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
}

var teamLines = []string{
	"AAAAteam001/T1.T1  " + at(0) + "  Alice Smith           hello team",
	"AAAAteam001/T1.r1  " + at(5) + "  Bob Jones             ↳ hi alice",
	"AAAAteam001/T2.T2  " + at(10) + "  Me Myself             (attachment: report.pdf)",
	"AAAAteam001/T1.r2  " + at(15) + "  users/6               ↳ late reply",
	"AAAAteam001/T3.T3  " + at(20) + "  Alice Smith           (deleted)",
}

func TestListCommand(t *testing.T) {
	tr := newTestRun(t)
	tr.f.pageMax = 2

	if got, want := tr.ok("list", "team"), lines(append(teamLines, "5 messages")...); got != want {
		t.Errorf("list:\ngot\n%s\nwant\n%s", got, want)
	}

	tr.f.calls = nil
	got := tr.ok("list", "-n", "2", "AAAAteam001")
	if want := lines(teamLines[3], teamLines[4], "2 messages (more; use -n to see more)"); got != want {
		t.Errorf("list -n 2:\ngot\n%s\nwant\n%s", got, want)
	}
	if n := strings.Count(strings.Join(tr.f.calls, "\n"), "/messages"); n != 1 {
		t.Errorf("list -n 2: %d message list calls, want 1: %v", n, tr.f.calls)
	}

	got = tr.ok("list", "-since", at(7), "team")
	if want := lines(teamLines[2], teamLines[3], teamLines[4], "3 messages"); got != want {
		t.Errorf("list -since:\ngot\n%s\nwant\n%s", got, want)
	}

	got = tr.ok("list", "-thread", "AAAAteam001/T1.r1")
	if want := lines(teamLines[0], teamLines[1], teamLines[3], "3 messages"); got != want {
		t.Errorf("list -thread:\ngot\n%s\nwant\n%s", got, want)
	}
	if got2 := tr.ok("list", "-thread", "AAAAteam001/T1.r1", "team"); got2 != got {
		t.Errorf("list -thread with space: got\n%s", got2)
	}

	got = tr.ok("list", "-grep", "(?i)HELLO|late", "team")
	if want := lines(teamLines[0], teamLines[3], "2 messages"); got != want {
		t.Errorf("list -grep:\ngot\n%s\nwant\n%s", got, want)
	}
	got = tr.ok("list", "-grep", "(?i)HELLO|late", "-n", "1", "team")
	if want := lines(teamLines[3], "1 messages (more may match; use -n to see more)"); got != want {
		t.Errorf("list -grep -n 1:\ngot\n%s\nwant\n%s", got, want)
	}

	if got := tr.ok("list", "project x"); got != "0 messages\n" {
		t.Errorf("list empty space: got %q", got)
	}
}

func TestListErrors(t *testing.T) {
	tr := newTestRun(t)
	tr.usage("list")
	tr.usage("list", "team", "extra")
	tr.fails("-n must be 0 or more", "list", "-n", "-1", "team")
	tr.fails("bad time: yesterday", "list", "-since", "yesterday", "team")
	tr.fails("-grep: error parsing regexp", "list", "-grep", "(", "team")
	tr.fails("bad message id: T1.r1", "list", "-thread", "T1.r1")
	tr.fails("no such message: AAAAteam001/T9.T9", "list", "-thread", "AAAAteam001/T9.T9")
	tr.fails("AAAAteam001/T1.r1 is in a different space", "list", "-thread", "AAAAteam001/T1.r1", "project x")
	tr.fails("no such space: nowhere", "list", "nowhere")
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"30m":                  now.Add(-30 * time.Minute),
		"12h":                  now.Add(-12 * time.Hour),
		"2d":                   now.Add(-48 * time.Hour),
		"1w":                   now.Add(-7 * 24 * time.Hour),
		"2026-11-20":           time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local),
		"2026-11-20 08:30":     time.Date(2026, 11, 20, 8, 30, 0, 0, time.Local),
		"2026-11-20T08:30:00Z": time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC),
	}
	for in, want := range cases {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q): got %v, %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "d", "-2d", "2x", "11/20"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q): expected error", bad)
		}
	}
}

func TestMessageFilter(t *testing.T) {
	since := time.Date(2026, 11, 20, 1, 2, 3, 0, time.FixedZone("x", -8*3600))
	got := messageFilter(since, "spaces/S/threads/T")
	want := `createTime > "2026-11-20T09:02:03Z" AND thread.name = spaces/S/threads/T`
	if got != want {
		t.Errorf("messageFilter: got %q, want %q", got, want)
	}
	if got := messageFilter(time.Time{}, ""); got != "" {
		t.Errorf("messageFilter empty: got %q", got)
	}
}
