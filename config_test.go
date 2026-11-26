package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := map[string]string{
		"~":            home,
		"~/token.json": filepath.Join(home, "token.json"),
		"~bob/x":       "~bob/x",
		"/abs/x":       "/abs/x",
		"rel/x":        "rel/x",
	}
	for in, want := range cases {
		if got := expandHome(in); got != want {
			t.Errorf("expandHome(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	cfg, args, err := loadConfig(flag.NewFlagSet("test", flag.ContinueOnError), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".gchat")
	if cfg.Credentials != filepath.Join(dir, "credentials.json") ||
		cfg.Token != filepath.Join(dir, "token.json") || cfg.Cache != filepath.Join(dir, "cache.json") ||
		!slices.Equal(args, []string{"a", "b"}) {
		t.Errorf("defaults: got %+v, %v", cfg, args)
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	hcl := "credentials = \"~/creds.json\"\ntoken = \"/tmp/tok.json\"\ncache = \"~/c.json\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gchat.hcl"), []byte(hcl), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = loadConfig(flag.NewFlagSet("test", flag.ContinueOnError),
		[]string{"-token", "flag.json"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Credentials != filepath.Join(home, "creds.json") || cfg.Token != "flag.json" ||
		cfg.Cache != filepath.Join(home, "c.json") {
		t.Errorf("file and flags: got %+v", cfg)
	}

	other := filepath.Join(t.TempDir(), "other.hcl")
	if err := os.WriteFile(other, []byte("token = \"other.json\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = loadConfig(flag.NewFlagSet("test", flag.ContinueOnError), []string{"-config", other})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "other.json" || cfg.Credentials != filepath.Join(dir, "credentials.json") {
		t.Errorf("-config: got %+v", cfg)
	}

	_, _, err = loadConfig(flag.NewFlagSet("test", flag.ContinueOnError),
		[]string{"-config", filepath.Join(t.TempDir(), "none.hcl")})
	if err == nil {
		t.Error("-config missing file: expected error")
	}
}

func TestReadConfigError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.hcl")
	if err := os.WriteFile(bad, []byte("nope = \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig([]string{bad}); err == nil {
		t.Error("readConfig: expected error")
	}
	if cfg, err := readConfig([]string{filepath.Join(t.TempDir(), "none.hcl")}); cfg != nil || err != nil {
		t.Errorf("readConfig missing: got %v, %v", cfg, err)
	}

	dir := t.TempDir()
	unreadable := filepath.Join(dir, "gchat.hcl")
	if err := os.Mkdir(unreadable, 0700); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.hcl")
	if err := os.WriteFile(good, []byte("token = \"good.json\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := readConfig([]string{unreadable, good}); err == nil || cfg != nil {
		t.Errorf("readConfig unreadable: got %+v, %v", cfg, err)
	}
	_, _, err := loadConfig(flag.NewFlagSet("test", flag.ContinueOnError), []string{"-config", unreadable})
	if err == nil || strings.Contains(err.Error(), "not found") {
		t.Errorf("-config unreadable: got %v", err)
	}
}

func TestShowConfig(t *testing.T) {
	var out bytes.Buffer
	cfg := &config{Credentials: "/c.json", Token: "/t.json", Cache: "/cache.json", out: &out}
	if err := showConfig(t.Context(), cfg, nil); err != nil {
		t.Fatal(err)
	}
	want := "credentials: /c.json\ntoken:       /t.json\ncache:       /cache.json\n"
	if out.String() != want {
		t.Errorf("showConfig: got %q, want %q", out.String(), want)
	}
	if err := showConfig(t.Context(), cfg, []string{"x"}); err == nil {
		t.Error("showConfig with args: expected usage error")
	}
}
