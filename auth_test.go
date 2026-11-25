package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "token.json")
	tok := &oauth2.Token{AccessToken: "access", RefreshToken: "refresh", TokenType: "Bearer"}
	if err := writeToken(path, tok); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("writeToken: mode %v, want 0600", fi.Mode().Perm())
	}
	got, err := readToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "access" || got.RefreshToken != "refresh" {
		t.Errorf("readToken: got %+v", got)
	}

	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(path); err == nil {
		t.Error("readToken bad json: expected error")
	}
}

type tokenSequence []*oauth2.Token

func (ts *tokenSequence) Token() (*oauth2.Token, error) {
	tok := (*ts)[0]
	*ts = (*ts)[1:]
	return tok, nil
}

func TestSavingTokenSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	seq := tokenSequence{{AccessToken: "one"}, {AccessToken: "two"}}
	s := &savingTokenSource{src: &seq, path: path, last: "one"}

	if _, err := s.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unchanged token was written: %v", err)
	}

	if _, err := s.Token(); err != nil {
		t.Fatal(err)
	}
	got, err := readToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "two" || s.last != "two" {
		t.Errorf("refreshed token: got %q, last %q", got.AccessToken, s.last)
	}
}

func TestOAuthScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	creds := `{"installed":{"client_id":"id","client_secret":"secret",` +
		`"auth_uri":"https://auth.example.com/","token_uri":"https://token.example.com/","redirect_uris":["http://localhost"]}}`
	if err := os.WriteFile(path, []byte(creds), 0600); err != nil {
		t.Fatal(err)
	}
	oc, err := (&config{Credentials: path}).oauthConfig()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(oc.Scopes, " ")
	for _, s := range []string{"chat.spaces.readonly", "chat.messages", "chat.memberships.readonly",
		"chat.messages.reactions", "chat.users.readstate", "directory.readonly", "userinfo.email"} {
		if !strings.Contains(got, "/auth/"+s) {
			t.Errorf("scopes: missing %s in %s", s, got)
		}
	}
}

func TestLogTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	var buf strings.Builder
	client := &http.Client{Transport: &logTransport{base: http.DefaultTransport, w: &buf}}
	resp, err := client.Get(srv.URL + "/v1/spaces?alt=json&prettyPrint=false&filter=spaceType+%3D+%22SPACE%22")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	got := buf.String()
	want := `GET /v1/spaces?filter=spaceType = "SPACE": 404 Not Found (`
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "ms)\n") {
		t.Errorf("logTransport: got %q, want prefix %q", got, want)
	}
}

func TestAuthorizeOutput(t *testing.T) {
	oc := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example.com/"}}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := authorize(t.Context(), oc, pw)
		done <- err
	}()

	r := bufio.NewReader(pr)
	var lines []string
	for range 3 {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}
	go io.Copy(io.Discard, r)
	if lines[0] != "open this URL in a browser to authorize gchat:" || lines[1] != "" {
		t.Fatalf("authorize: got %q", lines)
	}

	u, err := url.Parse(lines[2])
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	resp, err := http.Get(q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) +
		"&error=access_denied")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := <-done; err == nil || err.Error() != "authorization failed: access_denied" {
		t.Errorf("authorize: got %v", err)
	}
}

func TestReportAuth(t *testing.T) {
	pages := map[string]string{
		"":   `{"spaces":[{"name":"spaces/a"},{"name":"spaces/b"}],"nextPageToken":"p2"}`,
		"p2": `{"spaces":[{"name":"spaces/c"}]}`,
	}
	svc, client := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/userinfo":
			fmt.Fprint(w, `{"email":"me@example.com"}`)
		case "/v1/spaces":
			if r.URL.Query().Get("pageSize") != "1000" {
				http.Error(w, "bad pageSize", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, pages[r.URL.Query().Get("pageToken")])
		default:
			http.NotFound(w, r)
		}
	})
	base := strings.TrimSuffix(svc.BasePath, "/")
	saved := userinfoURL
	userinfoURL = base + "/userinfo"
	t.Cleanup(func() { userinfoURL = saved })

	var out bytes.Buffer
	cfg := &config{Token: "/t.json", client: client, chat: svc, out: &out, errOut: io.Discard}
	if err := reportAuth(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	want := "authorized as me@example.com (3 spaces); token saved to /t.json\n"
	if out.String() != want {
		t.Errorf("reportAuth: got %q, want %q", out.String(), want)
	}

	userinfoURL = base + "/missing"
	if err := reportAuth(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("reportAuth userinfo 404: got %v", err)
	}
}
