//go:build !windows && !slim

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tmuxcompat"
)

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

// TestTmuxLinkNeverStartsRealTmuxOnTheShimSocket runs calls that name the
// shim's own socket with -S while TMUX is unset, as a tool outside tuios
// does. Control mode (-C) and a flag the shim cannot parse used to send such
// a call to the real tmux, which then started a server on the shim's socket
// path. None of them may reach the real tmux.
//
// Negative control: with the InShimDir check cut from runAsTmux, the -X and
// the pane-socket cases reach the real tmux and fail here.
func TestTmuxLinkNeverStartsRealTmuxOnTheShimSocket(t *testing.T) {
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
	t.Setenv("PATH", realDir)
	t.Setenv("TMUX", "")
	t.Setenv("TUIOS_SESSION", "")
	t.Setenv("TUIOS_PANE_ID", "")
	sock := tmuxcompat.SocketPath(dir)
	for _, args := range [][]string{
		{"-S", sock, "-C", "attach-session", "-t", "$0", "-f", "ignore-size,read-only"},
		{"-S", sock, "-CC"},
		{"-S", sock, "-X", "list-sessions"},
		{"-S" + sock, "-X"},
		{"-S", filepath.Join(dir, "p", "1.sock"), "-X", "ls"},
	} {
		_ = os.Remove(record)
		if code := runAsTmux(args); code == 7 {
			t.Errorf("%q reached the real tmux", args)
		}
		if _, err := os.Stat(record); err == nil {
			t.Errorf("%q ran the real tmux", args)
		}
	}
}
