package main

import (
	"strings"
	"testing"
)

func TestGetCommand(t *testing.T) {
	tr := newTestRun(t)
	got := tr.ok("get", "AAAAteam001/T2.T2", "spaces/AAAAteam001/messages/T1.r2", "AAAAteam001/T3.T3")
	want := lines(
		"id:         AAAAteam001/T2.T2",
		"from:       Me Myself <me@example.com>",
		"date:       "+at(10),
		"reactions:  👍 2, :shipit: 1",
		"attachment: report.pdf (application/pdf)",
		"",
		"id:         AAAAteam001/T1.r2",
		"from:       users/6",
		"date:       "+at(15),
		"edited:     "+at(16),
		"thread:     AAAAteam001/T1",
		"",
		"late reply",
		"",
		"id:         AAAAteam001/T3.T3",
		"from:       Alice Smith <alice@example.com>",
		"date:       "+at(20),
		"deleted:    "+at(21))
	if got != want {
		t.Errorf("get:\ngot\n%s\nwant\n%s", got, want)
	}

	got = tr.ok("get", "AAAAteam001/T1.T1")
	if !strings.HasSuffix(got, "\n\nhello team\nsecond line\n") {
		t.Errorf("get multi-line text: got %q", got)
	}
}

func TestGetErrors(t *testing.T) {
	tr := newTestRun(t)
	tr.usage("get")
	tr.fails("bad message id: T1.T1", "get", "T1.T1")
	tr.fails("bad message id: spaces/AAAAteam001/threads/T1", "get", "spaces/AAAAteam001/threads/T1")

	out, errOut, err := tr.run("get", "AAAAteam001/T9.T9", "AAAAteam001/T1.r1")
	if !strings.Contains(out, "hi alice") ||
		errOut != "skipping: spaces/AAAAteam001/messages/T9.T9: no such message: AAAAteam001/T9.T9\n" ||
		err == nil || err.Error() != "1 of 2 failed" {
		t.Errorf("get missing: got %q, %q, %v", out, errOut, err)
	}
}

func TestMessageName(t *testing.T) {
	cases := map[string]string{
		"AAAA/T.K":                 "spaces/AAAA/messages/T.K",
		"spaces/AAAA/messages/T.K": "spaces/AAAA/messages/T.K",
	}
	for in, want := range cases {
		if got, err := messageName(in); err != nil || got != want {
			t.Errorf("messageName(%q): got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "T.K", "/T.K", "AAAA/", "a/b/c", "spaces/AAAA/messages/", "x/AAAA/messages/T"} {
		if _, err := messageName(bad); err == nil {
			t.Errorf("messageName(%q): expected error", bad)
		}
	}
}

func TestSummary(t *testing.T) {
	cases := map[string]string{
		"  first   line \nsecond": "first line",
		"\n\n  \nthird":           "third",
	}
	for in, want := range cases {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q): got %q, want %q", in, got, want)
		}
	}
	if got := fit("abcdef", 4); got != "abc…" {
		t.Errorf("fit: got %q", got)
	}
	if got := fit("ab", 4); got != "ab  " {
		t.Errorf("fit pad: got %q", got)
	}
}
