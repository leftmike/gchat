package main

import (
	"strings"
	"testing"
)

func TestMembersCommand(t *testing.T) {
	tr := newTestRun(t)
	tr.f.pageMax = 2
	got := tr.ok("members", "team")
	want := lines(
		"owner    Bob Jones                 bob@example.com",
		"manager  Alice Smith               alice@example.com",
		"member   Me Myself                 me@example.com",
		"-        groups/eng                (google group)",
		"4 members")
	if got != want {
		t.Errorf("members team:\ngot\n%s\nwant\n%s", got, want)
	}

	got = tr.ok("members", "AAAAgroup01")
	want = lines(
		"member   Alice Smith               alice@example.com",
		"member   Bob Jones                 bob@example.com",
		"member   Carol White               carol@example.com",
		"member   Dave Brown                dave@example.com",
		"member   Me Myself                 me@example.com",
		"member   users/6",
		"member   users/9                   (app)",
		"7 members")
	if got != want {
		t.Errorf("members group:\ngot\n%s\nwant\n%s", got, want)
	}

	tr.f.members["spaces/AAAAgroup01"] = []string{"users/1", "users/4"}
	tr.ok("members", "AAAAgroup01")
	tr.f.calls = nil
	got = tr.ok("spaces", "-type", "group")
	if !strings.Contains(got, "  Carol White\n") || memberCalls(tr.f) != 0 {
		t.Errorf("members updates cache: got %q, calls %v", got, tr.f.calls)
	}

	tr.usage("members")
	tr.usage("members", "team", "x")
	tr.fails("no such space: nowhere", "members", "nowhere")
	tr.f.membersFail = map[string]bool{"spaces/AAAAteam001": true}
	tr.fails("403", "members", "team")
}

func TestThreadCommand(t *testing.T) {
	tr := newTestRun(t)
	tr.f.pageMax = 2
	got := tr.ok("thread", "AAAAteam001/T1.r1")
	want := lines(
		"id:         AAAAteam001/T1.T1",
		"from:       Alice Smith <alice@example.com>",
		"date:       "+at(0),
		"",
		"hello team",
		"second line",
		"",
		"id:         AAAAteam001/T1.r1",
		"from:       Bob Jones <bob@example.com>",
		"date:       "+at(5),
		"thread:     AAAAteam001/T1",
		"",
		"hi alice",
		"",
		"id:         AAAAteam001/T1.r2",
		"from:       users/6",
		"date:       "+at(15),
		"edited:     "+at(16),
		"thread:     AAAAteam001/T1",
		"",
		"late reply")
	if got != want {
		t.Errorf("thread:\ngot\n%s\nwant\n%s", got, want)
	}

	if got := tr.ok("thread", "spaces/AAAAteam001/messages/T2.T2"); !strings.HasPrefix(got, "id:         AAAAteam001/T2.T2\n") ||
		strings.Count(got, "id:") != 1 {
		t.Errorf("thread of one: got %q", got)
	}

	tr.usage("thread")
	tr.usage("thread", "AAAAteam001/T1.r1", "AAAAteam001/T2.T2")
	tr.fails("bad message id: T1.r1", "thread", "T1.r1")
	tr.fails("no such message: AAAAteam001/T9.T9", "thread", "AAAAteam001/T9.T9")
	tr.f.messages[1].Thread = nil
	tr.fails("AAAAteam001/T1.r1 isn't in a thread", "thread", "AAAAteam001/T1.r1")
}
