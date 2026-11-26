package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/chat/v1"
)

type fakeChat struct {
	mu      sync.Mutex
	email   string
	spaces  []*chat.Space
	dms     map[string]string
	users   map[string]*chat.User
	members map[string][]string

	membersFail map[string]bool
	messages    []*chat.Message
	pageMax     int
	failNext    int
	calls       []string
	svc         *chat.Service
	client      *http.Client
}

func newFakeChat(t *testing.T) *fakeChat {
	t.Helper()
	day := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	f := &fakeChat{
		email:   "me@example.com",
		pageMax: maxPageSize,
		spaces: []*chat.Space{
			{Name: "spaces/AAAAteam001", DisplayName: "Team", SpaceType: "SPACE",
				LastActiveTime: day.Add(2 * time.Hour).Format(time.RFC3339)},
			{Name: "spaces/AAAAdm00001", SpaceType: "DIRECT_MESSAGE",
				LastActiveTime: day.Add(time.Hour).Format(time.RFC3339)},
			{Name: "spaces/AAAAgroup01", SpaceType: "GROUP_CHAT",
				LastActiveTime: day.Format(time.RFC3339)},
			{Name: "spaces/AAAAproj001", DisplayName: "Project X", SpaceType: "SPACE",
				LastActiveTime: day.Add(3 * time.Hour).Format(time.RFC3339)},
			{Name: "spaces/AAAAproj002", DisplayName: "Project Y", SpaceType: "SPACE",
				LastActiveTime: "1970-01-01T00:00:00Z"},
		},
		dms: map[string]string{"alice@example.com": "spaces/AAAAdm00001"},
		users: map[string]*chat.User{
			"users/1": {Name: "users/1", DisplayName: "Me Myself", Email: "me@example.com", Type: "HUMAN"},
			"users/2": {Name: "users/2", DisplayName: "Alice Smith", Email: "alice@example.com", Type: "HUMAN"},
			"users/3": {Name: "users/3", DisplayName: "Bob Jones", Email: "bob@example.com", Type: "HUMAN"},
			"users/4": {Name: "users/4", DisplayName: "Carol White", Email: "carol@example.com", Type: "HUMAN"},
			"users/5": {Name: "users/5", DisplayName: "Dave Brown", Email: "dave@example.com", Type: "HUMAN"},
			"users/6": {Name: "users/6", Type: "HUMAN"},
			"users/9": {Name: "users/9", Type: "BOT"},
		},
		members: map[string][]string{
			"spaces/AAAAteam001": {"users/1", "users/2", "users/3"},
			"spaces/AAAAdm00001": {"users/2", "users/1", "users/9"},
			"spaces/AAAAgroup01": {"users/1", "users/2", "users/3", "users/4", "users/5", "users/6", "users/9"},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /userinfo", f.userinfo)
	mux.HandleFunc("GET /v1/spaces", f.listSpaces)
	mux.HandleFunc("GET /v1/spaces/{space}", f.getSpace)
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", f.findDirectMessage)
	mux.HandleFunc("GET /v1/spaces/{space}/members", f.listMembers)
	mux.HandleFunc("GET /v1/spaces/{space}/messages", f.listMessages)
	mux.HandleFunc("GET /v1/spaces/{space}/messages/{message}", f.getMessage)
	f.svc, f.client = newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if f.failNext > 0 && strings.HasPrefix(r.URL.Path, "/v1/") {
			f.failNext--
			http.Error(w, `{"error":{"code":503,"message":"unavailable"}}`, http.StatusServiceUnavailable)
			return
		}
		mux.ServeHTTP(w, r)
	})

	msg := func(id, sender string, min int, text string, reply bool) *chat.Message {
		thread, _, _ := strings.Cut(id, ".")
		return &chat.Message{Name: "spaces/AAAAteam001/messages/" + id, Sender: f.users[sender],
			CreateTime: day.Add(time.Duration(min) * time.Minute).Format(time.RFC3339Nano), Text: text,
			Thread:      &chat.Thread{Name: "spaces/AAAAteam001/threads/" + thread},
			ThreadReply: reply, Space: &chat.Space{Name: "spaces/AAAAteam001"}}
	}
	f.messages = []*chat.Message{
		msg("T1.T1", "users/2", 0, "hello team\nsecond line", false),
		msg("T1.r1", "users/3", 5, "hi alice", true),
		msg("T2.T2", "users/1", 10, "", false),
		msg("T1.r2", "users/6", 15, "late reply", true),
		msg("T3.T3", "users/2", 20, "", false),
	}
	f.messages[2].Attachment = []*chat.Attachment{{ContentName: "report.pdf", ContentType: "application/pdf"}}
	f.messages[2].EmojiReactionSummaries = []*chat.EmojiReactionSummary{
		{Emoji: &chat.Emoji{Unicode: "👍"}, ReactionCount: 2},
		{Emoji: &chat.Emoji{CustomEmoji: &chat.CustomEmoji{EmojiName: "shipit"}}, ReactionCount: 1}}
	f.messages[3].LastUpdateTime = day.Add(16 * time.Minute).Format(time.RFC3339Nano)
	f.messages[4].DeletionMetadata = &chat.DeletionMetadata{DeletionType: "CREATOR"}
	f.messages[4].DeleteTime = day.Add(21 * time.Minute).Format(time.RFC3339Nano)

	saved := userinfoURL
	userinfoURL = strings.TrimSuffix(f.svc.BasePath, "/") + "/userinfo"
	t.Cleanup(func() { userinfoURL = saved })
	return f
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func replyError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func page[T any](f *fakeChat, w http.ResponseWriter, r *http.Request, items []T) ([]T, string, bool) {
	q := r.URL.Query()
	if q.Has("pageToken") && q.Get("pageToken") == "" {
		replyError(w, http.StatusBadRequest, "empty pageToken")
		return nil, "", false
	}
	size := f.pageMax
	if s := q.Get("pageSize"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			replyError(w, http.StatusBadRequest, "bad pageSize")
			return nil, "", false
		}
		if n > 0 {
			size = min(n, f.pageMax)
		}
	}
	start := 0
	if tok := q.Get("pageToken"); tok != "" {
		n, err := strconv.Atoi(tok)
		if err != nil || n < 0 || n > len(items) {
			replyError(w, http.StatusBadRequest, "bad pageToken")
			return nil, "", false
		}
		start = n
	}
	end := min(start+size, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[start:end], next, true
}

func (f *fakeChat) space(name string) *chat.Space {
	for _, s := range f.spaces {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (f *fakeChat) userinfo(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]string{"email": f.email})
}

func (f *fakeChat) listSpaces(w http.ResponseWriter, r *http.Request) {
	all := f.spaces
	if filter := r.URL.Query().Get("filter"); filter != "" {
		all = nil
		for _, s := range f.spaces {
			if strings.Contains(filter, fmt.Sprintf("spaceType = %q", s.SpaceType)) {
				all = append(all, s)
			}
		}
	}
	spaces, next, ok := page(f, w, r, all)
	if ok {
		reply(w, chat.ListSpacesResponse{Spaces: spaces, NextPageToken: next})
	}
}

func (f *fakeChat) getSpace(w http.ResponseWriter, r *http.Request) {
	s := f.space("spaces/" + r.PathValue("space"))
	if s == nil {
		replyError(w, http.StatusForbidden, "permission denied")
		return
	}
	reply(w, s)
}

func (f *fakeChat) findDirectMessage(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimPrefix(r.URL.Query().Get("name"), "users/")
	if !strings.HasSuffix(email, "@example.com") {
		replyError(w, http.StatusBadRequest, "Missing or malformed user resource name")
		return
	}
	name, ok := f.dms[email]
	if !ok {
		replyError(w, http.StatusNotFound, "direct message not found")
		return
	}
	reply(w, f.space(name))
}

func (f *fakeChat) listMembers(w http.ResponseWriter, r *http.Request) {
	space := "spaces/" + r.PathValue("space")
	if f.space(space) == nil || f.membersFail[space] {
		replyError(w, http.StatusForbidden, "permission denied")
		return
	}
	var all []*chat.Membership
	for _, id := range f.members[space] {
		all = append(all, &chat.Membership{Name: space + "/members/" + strings.TrimPrefix(id, "users/"),
			Member: f.users[id], State: "JOINED", Role: "ROLE_MEMBER"})
	}
	ms, next, ok := page(f, w, r, all)
	if ok {
		reply(w, chat.ListMembershipsResponse{Memberships: ms, NextPageToken: next})
	}
}

var (
	createAfter = regexp.MustCompile(`createTime > "([^"]+)"`)
	threadIs    = regexp.MustCompile(`thread.name = (\S+)`)
)

func (f *fakeChat) listMessages(w http.ResponseWriter, r *http.Request) {
	space := "spaces/" + r.PathValue("space")
	if f.space(space) == nil {
		replyError(w, http.StatusForbidden, "permission denied")
		return
	}
	q := r.URL.Query()
	filter := q.Get("filter")
	var after time.Time
	if m := createAfter.FindStringSubmatch(filter); m != nil {
		var err error
		if after, err = time.Parse(time.RFC3339, m[1]); err != nil {
			replyError(w, http.StatusBadRequest, "bad createTime")
			return
		}
	}
	thread := ""
	if m := threadIs.FindStringSubmatch(filter); m != nil {
		thread = m[1]
	}
	var all []*chat.Message
	for _, m := range f.messages {
		created, _ := time.Parse(time.RFC3339Nano, m.CreateTime)
		if spaceOf(m.Name) != space || (!after.IsZero() && !created.After(after)) ||
			(thread != "" && m.Thread.Name != thread) {
			continue
		}
		all = append(all, m)
	}
	switch q.Get("orderBy") {
	case "createTime desc":
		slices.Reverse(all)
	case "", "createTime asc", "createTime":
	default:
		replyError(w, http.StatusBadRequest, "bad orderBy")
		return
	}
	msgs, next, ok := page(f, w, r, all)
	if ok {
		reply(w, chat.ListMessagesResponse{Messages: msgs, NextPageToken: next})
	}
}

func (f *fakeChat) getMessage(w http.ResponseWriter, r *http.Request) {
	name := "spaces/" + r.PathValue("space") + "/messages/" + r.PathValue("message")
	for _, m := range f.messages {
		if m.Name == name {
			reply(w, m)
			return
		}
	}
	replyError(w, http.StatusNotFound, "message not found")
}

type testRun struct {
	t     *testing.T
	f     *fakeChat
	token string
	cache string
}

func newTestRun(t *testing.T) *testRun {
	return &testRun{t: t, f: newFakeChat(t), token: "/home/me/.gchat/token.json",
		cache: filepath.Join(t.TempDir(), "cache.json")}
}

func (tr *testRun) config(out, errOut *bytes.Buffer, name string) *config {
	return &config{
		Credentials: "/home/me/.gchat/credentials.json",
		Token:       tr.token,
		Cache:       tr.cache,
		client:      tr.f.client,
		chat:        tr.f.svc,
		out:         out,
		errOut:      errOut,
		name:        "gchat " + name,
		program:     "gchat",
	}
}

func (tr *testRun) run(args ...string) (string, string, error) {
	tr.t.Helper()
	var out, errOut bytes.Buffer
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(&errOut)
	run := cmds[args[0]].setup(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return out.String(), errOut.String(), err
	}
	err := run(tr.t.Context(), tr.config(&out, &errOut, args[0]), fs.Args())
	return out.String(), errOut.String(), err
}

func (tr *testRun) ok(args ...string) string {
	tr.t.Helper()
	out, errOut, err := tr.run(args...)
	if err != nil || errOut != "" {
		tr.t.Fatalf("%v: %v, stderr %q", args, err, errOut)
	}
	return out
}

func (tr *testRun) fails(want string, args ...string) {
	tr.t.Helper()
	_, _, err := tr.run(args...)
	if err == nil || !strings.Contains(err.Error(), want) {
		tr.t.Errorf("%v: got error %v, want %q", args, err, want)
	}
}

func (tr *testRun) usage(args ...string) {
	tr.t.Helper()
	_, _, err := tr.run(args...)
	var u usageError
	if !errors.As(err, &u) {
		tr.t.Errorf("%v: got %v, want usage error", args, err)
	}
}

func lines(s ...string) string {
	return strings.Join(s, "\n") + "\n"
}

func TestConfigCommand(t *testing.T) {
	tr := newTestRun(t)
	got := tr.ok("config")
	want := lines("credentials: /home/me/.gchat/credentials.json", "token:       /home/me/.gchat/token.json",
		"cache:       "+tr.cache)
	if got != want {
		t.Errorf("config: got %q, want %q", got, want)
	}
	tr.usage("config", "x")
	tr.usage("auth", "x")
}

func TestReportAuth(t *testing.T) {
	tr := newTestRun(t)
	tr.f.pageMax = 2
	var out, errOut bytes.Buffer
	if err := reportAuth(t.Context(), tr.config(&out, &errOut, "auth")); err != nil {
		t.Fatal(err)
	}
	want := "authorized as me@example.com (5 spaces); token saved to /home/me/.gchat/token.json\n"
	if out.String() != want || errOut.String() != "" {
		t.Errorf("reportAuth: got %q, stderr %q", out.String(), errOut.String())
	}
	if n := strings.Count(strings.Join(tr.f.calls, "\n"), "GET /v1/spaces"); n != 3 {
		t.Errorf("reportAuth: %d spaces.list calls, want 3: %v", n, tr.f.calls)
	}
}

func TestReportAuthErrors(t *testing.T) {
	tr := newTestRun(t)
	saved := userinfoURL
	userinfoURL = strings.TrimSuffix(tr.f.svc.BasePath, "/") + "/missing"
	var out, errOut bytes.Buffer
	if err := reportAuth(t.Context(), tr.config(&out, &errOut, "auth")); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Errorf("reportAuth userinfo 404: got %v", err)
	}
	userinfoURL = saved

	tr = newTestRun(t)
	tr.f.failNext = maxRetries + 1
	out.Reset()
	if err := reportAuth(t.Context(), tr.config(&out, &errOut, "auth")); err == nil ||
		!strings.Contains(err.Error(), "listing spaces") {
		t.Errorf("reportAuth spaces 503: got %v", err)
	}
}

func TestVerboseRetry(t *testing.T) {
	tr := newTestRun(t)
	tr.f.failNext = 2
	verbose = true
	t.Cleanup(func() { verbose = false })
	var out, errOut bytes.Buffer
	if err := reportAuth(t.Context(), tr.config(&out, &errOut, "auth")); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(errOut.String(), "retrying in "); n != 2 {
		t.Errorf("verbose retry: stderr %q", errOut.String())
	}
}

func TestFakePaging(t *testing.T) {
	tr := newTestRun(t)
	tr.f.pageMax = 1
	resp, err := tr.f.svc.Spaces.List().PageSize(5).PageToken("1").Context(t.Context()).Do()
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Spaces) != 1 || resp.Spaces[0].Name != "spaces/AAAAdm00001" || resp.NextPageToken != "2" {
		t.Errorf("paging: got %+v", resp)
	}
	if _, err := tr.f.svc.Spaces.Get("spaces/nope").Context(t.Context()).Do(); err == nil {
		t.Error("get missing space: expected error")
	}
	sp, err := tr.f.svc.Spaces.Get("spaces/AAAAteam001").Context(t.Context()).Do()
	if err != nil || sp.DisplayName != "Team" {
		t.Errorf("get space: got %+v, %v", sp, err)
	}
}
