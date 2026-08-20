package main

import (
	"strings"
	"testing"
)

// The control verbs are only reachable through these subcommands, and a verb
// with no command in front of it is a verb nobody can call. Cobra registers by
// value at build time, so a command declared and never added to the root
// compiles, passes vet, and is simply absent from the binary. This pins that
// every one of them is there and that its flags are the ones the verb takes.
func TestControlCommandsAreRegistered(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		flags   []string
	}{
		{
			command: "list-options",
			args:    []string{"list-options", "appearance.", "--section", "appearance", "--json", "-s", "work"},
			flags:   []string{"section", "json", "session"},
		},
		{
			command: "focus-window",
			args:    []string{"focus-window", "--relative", "next", "--json"},
			flags:   []string{"relative", "direction", "json", "session"},
		},
		{
			command: "move-window",
			args:    []string{"move-window", "2", "-w", "build", "--follow", "--json"},
			flags:   []string{"window", "follow", "json", "session"},
		},
		{
			command: "set-window",
			args:    []string{"set-window", "-w", "build", "--name", "api", "--minimize", "--json"},
			flags:   []string{"window", "name", "minimize", "restore", "json", "session"},
		},
		{
			command: "select-workspace",
			args:    []string{"select-workspace", "2", "--json"},
			flags:   []string{"json", "session"},
		},
		{
			command: "list-workspaces",
			args:    []string{"list-workspaces", "--json"},
			flags:   []string{"json", "session"},
		},
		{
			command: "split-window",
			args:    []string{"split-window", "vertical", "-w", "build", "--name", "logs", "--json"},
			flags:   []string{"window", "name", "json", "session"},
		},
		{
			command: "set-layout",
			args:    []string{"set-layout", "--tiling", "true", "--equalize", "--rotate", "--json"},
			flags:   []string{"tiling", "equalize", "rotate", "json", "session"},
		},
		{
			command: "new-window",
			args:    []string{"new-window", "build", "--workspace", "2", "--cwd", "/tmp", "--no-focus", "--json"},
			flags:   []string{"workspace", "cwd", "no-focus", "json", "session"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			root := newRootCommand()
			cmd, rest, err := root.Find(tc.args)
			if err != nil {
				t.Fatalf("Find(%v): %v", tc.args, err)
			}
			if cmd.Name() != tc.command {
				t.Fatalf("%q resolved to %q, so it is not registered on the root", tc.command, cmd.Name())
			}
			for _, flag := range tc.flags {
				if cmd.Flags().Lookup(flag) == nil {
					t.Errorf("%s has no --%s flag", tc.command, flag)
				}
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Fatalf("%s failed to parse %v: %v", tc.command, rest, err)
			}
			if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
				t.Fatalf("%s rejected its own example arguments: %v", tc.command, err)
			}
		})
	}
}

// set-window's two state flags are a single boolean spelled two ways, so asking
// for both is a contradiction the command has to refuse. It is checked before
// anything dials the daemon, which is what makes it testable here.
func TestSetWindowRefusesMinimizeAndRestoreTogether(t *testing.T) {
	root := newRootCommand()
	root.SetArgs([]string{"set-window", "--minimize", "--restore"})
	root.SilenceUsage, root.SilenceErrors = true, true

	err := root.Execute()
	if err == nil {
		t.Fatal("set-window accepted --minimize and --restore together")
	}
	if !strings.Contains(err.Error(), "--minimize and --restore") {
		t.Fatalf("set-window failed for the wrong reason: %v", err)
	}
}

// --tiling carries a value rather than being a plain switch, because the verb
// distinguishes "leave tiling alone" from "turn it off". A value that is not a
// boolean is rejected here rather than sent as a string the daemon will refuse.
func TestSetLayoutRejectsNonBooleanTiling(t *testing.T) {
	root := newRootCommand()
	root.SetArgs([]string{"set-layout", "--tiling", "maybe"})
	root.SilenceUsage, root.SilenceErrors = true, true

	err := root.Execute()
	if err == nil {
		t.Fatal("set-layout accepted a non-boolean --tiling value")
	}
	if !strings.Contains(err.Error(), "--tiling takes true or false") {
		t.Fatalf("set-layout failed for the wrong reason: %v", err)
	}
}
