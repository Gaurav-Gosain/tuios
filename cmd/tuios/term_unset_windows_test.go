//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestStartsOnAConsoleWithNoTerm runs the real tuios.exe on a real pseudo
// console with TERM unset, the state Windows Terminal and conhost leave it in
// (#1433). The E2E suite does not run on Windows, so the windows job in
// go-test.yml runs this.
//
// Ways it could go wrong: the check refuses the empty TERM; something later on
// the same path assumes TERM and the TUI never draws; or the pane inherits the
// host's empty TERM, or a dumb one, and every program in it loses its colour.
// The pane's shell prints the TERM and COLORTERM it got, and the text can only
// reach the console through the TUI's own frame.
//
// A TERM set to dumb stays refused, on Windows as everywhere else.
func TestStartsOnAConsoleWithNoTerm(t *testing.T) {
	dir, err := os.MkdirTemp("", "tuios-term")
	if err != nil {
		t.Fatal(err)
	}
	// Not t.TempDir: a daemon that takes a moment to exit still holds the
	// executable, and a folder left behind on a runner is not a test failure.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	bin := filepath.Join(dir, "tuios.exe")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	shell := filepath.Join(dir, "pane.cmd")
	script := "@echo off\r\necho PANE-TERM=[%TERM%] PANE-COLORTERM=[%COLORTERM%]\r\ncmd.exe /k\r\n"
	if err := os.WriteFile(shell, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	env := []string{"XDG_RUNTIME_DIR=" + testutil.RuntimeDir(t), "SHELL=" + shell}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "TERM", "COLORTERM", "XDG_RUNTIME_DIR", "SHELL":
			continue
		}
		env = append(env, kv)
	}

	// The refused run starts the daemon too, so this waits for both subtests.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, bin, "kill-server")
		stop.Env = env
		if out, err := stop.CombinedOutput(); err != nil {
			t.Logf("kill-server: %v\n%s", err, out)
		}
	})

	t.Run("unset", func(t *testing.T) {
		c := openConsole(t, bin, env, "new", "term-unset")
		if out := c.wait(t, "new window", "TERM is not set"); strings.Contains(out, "TERM is not set") {
			t.Fatalf("tuios refused the unset TERM:\n%s", out)
		}
		if _, err := c.pty.Write([]byte("n")); err != nil {
			t.Fatal(err)
		}
		c.wait(t, "PANE-TERM=[xterm-256color] PANE-COLORTERM=[truecolor]")
	})

	t.Run("dumb", func(t *testing.T) {
		c := openConsole(t, bin, append(env[:len(env):len(env)], "TERM=dumb"), "new", "term-dumb")
		c.wait(t, `TERM is "dumb"`)
	})
}

// console is a program on a pseudo console, and all it has written so far.
type console struct {
	pty xpty.Pty

	mu   sync.Mutex
	seen []byte
}

func openConsole(t *testing.T, bin string, env []string, args ...string) *console {
	t.Helper()
	pty, cmd, err := ptyspawn.Spawn(120, 40, func() *exec.Cmd {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		return cmd
	}, t.Logf)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pty.Close()
	})
	c := &console{pty: pty}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			c.mu.Lock()
			c.seen = append(c.seen, buf[:n]...)
			c.mu.Unlock()
			// The pseudo console asks once at startup, and waits for the answer.
			if bytes.Contains(buf[:n], []byte("\x1b[c")) {
				_, _ = pty.Write([]byte("\x1b[?62;4;22c"))
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

// shown is what the console has written, with the escape sequences removed.
func (c *console) shown() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ansi.Strip(string(c.seen))
}

// wait returns what the console has shown once any of the markers is on it. A
// console stays open after its program ends, so the wait has a deadline.
func (c *console) wait(t *testing.T, markers ...string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out := c.shown()
		for _, m := range markers {
			if strings.Contains(out, m) {
				return out
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the console never showed any of %q; it showed:\n%s", markers, c.shown())
	return ""
}
