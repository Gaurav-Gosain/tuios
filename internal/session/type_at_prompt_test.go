package session

import (
	"path/filepath"
	"testing"
	"time"
)

// waitPrompt polls cond for up to five seconds.
func waitPrompt(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A client attached to a daemon session asks the daemon to type a cd into a
// pane. At a shell prompt it is typed. With a program in the foreground it is
// not, whatever the pane last reported.
func TestTypeAtPromptTypesOnlyAtAShellPrompt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "prompt")
	tui := attachTestClient(t, "prompt")

	win := sess.GetState().Windows[0]
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the window has no PTY")
	}
	if _, ok := readForegroundPGID(pty.ShellPID()); !ok {
		t.Skip("this platform does not report the terminal's foreground group")
	}
	waitPrompt(t, "the shell to take the terminal", func() bool { return shellAtPrompt(pty) })

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	typed, err := tui.CdAtPrompt(win.PTYID, dir, false)
	if err != nil || !typed {
		t.Fatalf("TypeAtPrompt at a prompt = %v, %v; want typed", typed, err)
	}
	waitPrompt(t, "the shell to move", func() bool {
		cwd, ok := pty.ProcessCwd()
		return ok && cwd == dir
	})

	// A program takes the terminal. The cd must not reach it.
	if _, err := pty.Write([]byte("sleep 30\n")); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, "sleep to take the terminal", func() bool { return !shellAtPrompt(pty) })
	typed, err = tui.CdAtPrompt(win.PTYID, "/", false)
	if typed {
		t.Fatalf("TypeAtPrompt under a program = %v, %v; want not typed", typed, err)
	}
}

// A daemon that did not offer the request gets none, and nothing is typed.
func TestTypeAtPromptFailsClosedOnAnOlderDaemon(t *testing.T) {
	c := NewTUIClient()
	if typed, err := c.CdAtPrompt("pty", "/", false); typed || err != ErrTypeAtPromptUnsupported {
		t.Fatalf("TypeAtPrompt on an older daemon = %v, %v", typed, err)
	}
}
