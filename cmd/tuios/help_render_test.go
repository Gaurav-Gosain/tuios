package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// TestEveryHelpRenders runs --help through fang, the way main does, for every
// command in the tree. fang styles each Example line word by word, and a word
// that is a bare "=" (as in a shell test, [ "$x" = yes ]) made it index an
// empty string and panic, so `tuios ask-human --help` crashed instead of
// printing. The skill sends agents to --help, so every one has to render.
func TestEveryHelpRenders(t *testing.T) {
	var paths [][]string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			p := append(append([]string(nil), path...), sub.Name())
			paths = append(paths, p)
			walk(sub, p)
		}
	}
	walk(newRootCommand(), nil)
	if len(paths) < 50 {
		t.Fatalf("found %d commands, want the whole tree", len(paths))
	}

	for _, path := range paths {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := newRootCommand()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(append(path, "--help"))
			if err := renderHelp(root); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "USAGE") {
				t.Errorf("--help printed no usage:\n%s", out.String())
			}
		})
	}
}

// renderHelp executes root through fang and turns a panic into an error.
func renderHelp(root *cobra.Command) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("--help panicked: %v", r)
		}
	}()
	return fang.Execute(context.Background(), root)
}
