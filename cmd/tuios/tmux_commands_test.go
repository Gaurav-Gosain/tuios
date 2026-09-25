//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tmuxcompat"
)

func TestIsTmuxName(t *testing.T) {
	for arg0, want := range map[string]bool{
		"tmux":                   true,
		"/run/tuios/bin/tmux":    true,
		"TMUX.exe":               true,
		"tuios":                  false,
		"/usr/local/bin/tuios":   false,
		"tmux-shim":              false,
		"/opt/tmux/bin/tuios-ts": false,
	} {
		if got := isTmuxName(arg0); got != want {
			t.Errorf("isTmuxName(%q) = %v, want %v", arg0, got, want)
		}
	}
}

// TestTmuxLinkHandsOtherCallsToRealTmux runs the binary's tmux entry point
// with TMUX naming a real server, as a shell started under tmux-shim that
// then attached to a real tmux would, and checks the call reaches the real
// tmux on PATH with its arguments untouched and its exit status kept. The
// link must never break a real tmux.
func TestTmuxLinkHandsOtherCallsToRealTmux(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "tlink")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	rt := filepath.Join(root, "rt")
	if err := os.Mkdir(rt, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + record + "\nexit 7\n"
	if err := os.WriteFile(filepath.Join(realDir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := tmuxShimDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmuxcompat.BinDir(dir)+string(os.PathListSeparator)+realDir)

	for _, tc := range []struct {
		name string
		tmux string
		args []string
	}{
		{"TMUX names a real server", "/tmp/tmux-501/default,99,0", []string{"list-panes", "-F", "#{pane_id}"}},
		{"TMUX unset", "", []string{"new-session", "-d"}},
		{"-L names a server", tmuxcompat.TmuxValue(dir, 1), []string{"-L", "swarm", "has-session", "-t", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(record)
			t.Setenv("TMUX", tc.tmux)
			if code := runAsTmux(tc.args); code != 7 {
				t.Errorf("exit = %d, want the real tmux's 7", code)
			}
			got, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("the real tmux never ran: %v", err)
			}
			if strings.TrimSpace(string(got)) != strings.Join(tc.args, "\n") {
				t.Errorf("the real tmux got %q, want %q", got, tc.args)
			}
		})
	}
}
