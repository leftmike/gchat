package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

type runFunc func(ctx context.Context, cfg *config, args []string) error

type cmd struct {
	setup func(fs *flag.FlagSet) runFunc
	help  string
}

type usageError string

func (u usageError) Error() string {
	return "usage: " + string(u)
}

func noFlags(run runFunc) func(fs *flag.FlagSet) runFunc {
	return func(fs *flag.FlagSet) runFunc {
		return run
	}
}

func fatal(name string, err error) {
	var u usageError
	if errors.As(err, &u) {
		fmt.Fprintf(os.Stderr, "usage: %s\n", strings.TrimSpace(name+" "+string(u)))
	} else {
		fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
	}
	os.Exit(1)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s <cmd> [flags] [args]\n", os.Args[0])
	for _, name := range slices.Sorted(maps.Keys(cmds)) {
		fmt.Fprintf(os.Stderr, "    %s: %s\n", name, cmds[name].help)
	}
}

var (
	verbose bool

	cmds = map[string]cmd{
		"auth":    {setup: authCmd, help: "authorize gchat with Google, or revoke the saved token"},
		"config":  {setup: noFlags(showConfig), help: "show the effective configuration"},
		"get":     {setup: noFlags(get), help: "show messages by id"},
		"help":    {help: "show this help"},
		"list":    {setup: listCmd, help: "list the messages in a space, oldest first"},
		"members": {setup: noFlags(members), help: "list the members of a space"},
		"spaces":  {setup: spacesCmd, help: "list spaces, group chats, and direct messages"},
		"thread":  {setup: noFlags(thread), help: "show every message in a message's thread"},
	}
)

func main() {
	if len(os.Args) >= 2 {
		if os.Args[1] == "help" {
			usage()
			return
		}
		if c, ok := cmds[os.Args[1]]; ok {
			name := os.Args[0] + " " + os.Args[1]
			fs := flag.NewFlagSet(name, flag.ExitOnError)
			run := c.setup(fs)
			cfg, args, err := loadConfig(fs, os.Args[2:])
			if err != nil {
				fatal(name, err)
			}
			cfg.out = os.Stdout
			cfg.errOut = os.Stderr
			cfg.name = name
			cfg.program = os.Args[0]

			if err := run(context.Background(), cfg, args); err != nil {
				fatal(name, err)
			}
			return
		}
	}

	usage()
	os.Exit(1)
}
