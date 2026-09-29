//go:build linux || darwin

package session

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// TestAttachFromOwnPaneIsRefused is #235 at the daemon: a client in a pane of
// the session it asks for is refused, with the pane's environment and without
// it. Without the environment the daemon places the client by its terminal.
func TestAttachFromOwnPaneIsRefused(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "nest")

	for name, prefix := range map[string]string{
		"with the pane environment":    "",
		"without the pane environment": "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID -u TUIOS_SESSION -u TUIOS_SOCKET -u TUIOS_PANE_TOKEN ",
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			runInPane(t, d, sess, b, prefix+helperCommand(t, sp, out, "attach", "nest"))
			got := waitHelper(t, out)
			if !strings.Contains(got, `You are inside session "nest"`) {
				t.Errorf("an attach from a pane of its own session got %q, want the refusal", got)
			}
		})
	}
	if n := d.getSessionClientCount(sess.ID); n != 0 {
		t.Errorf("a refused client still counts in the session: %d clients", n)
	}
}

// TestUnnamedAttachFromPaneIsRefused covers a bare attach from a pane: the
// daemon would pick the session, so the caller is asked to name one.
func TestUnnamedAttachFromPaneIsRefused(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "bare")
	out := filepath.Join(t.TempDir(), "out")
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "attach", ""))
	got := waitHelper(t, out)
	if !strings.Contains(got, `You are inside session "bare"`) || !strings.Contains(got, "tuios attach NAME") {
		t.Errorf("an unnamed attach from a pane got %q, want the refusal that asks for a name", got)
	}
}

// TestAttachFromPaneToOtherSessionIsAllowed is the case that stays open: a
// pane of one session shows another.
func TestAttachFromPaneToOtherSessionIsAllowed(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "here")
	makeSessionWithWindow(t, d, "there")
	out := filepath.Join(t.TempDir(), "out")
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "attach", "there"))
	if got := waitHelper(t, out); !strings.HasPrefix(got, "nonce:") {
		t.Errorf("an attach from a pane to another session got %q, want it attached", got)
	}
}

// TestServedAndForcedAttachFromOwnPaneAreAllowed covers the two ways past the
// refusal: a served client (tuios-web, the SSH server) takes its size from a
// remote viewer, and a forced attach asked for it.
func TestServedAndForcedAttachFromOwnPaneAreAllowed(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "pass")
	for _, mode := range []string{"attach-served", "attach-force"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			runInPane(t, d, sess, b, helperCommand(t, sp, out, mode, "pass"))
			if got := waitHelper(t, out); !strings.HasPrefix(got, "nonce:") {
				t.Errorf("a %s from the session's own pane got %q, want it attached", mode, got)
			}
		})
	}
}

// startHelperOnOwnTerminal starts the helper from this test process, outside
// every pane, on a PTY of its own, with env added to its environment.
func startHelperOnOwnTerminal(t *testing.T, sp, mode, args string, env ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out")
	pty, cmd, err := ptyspawn.Spawn(80, 24, func() *exec.Cmd {
		c := exec.Command(exe, "-test.run=^TestHelperSocketCaller$")
		c.Env = append(os.Environ(), helperSockEnv+"="+sp, helperOutEnv+"="+out,
			helperModeEnv+"="+mode, helperArgsEnv+"="+args)
		c.Env = append(c.Env, env...)
		return c
	}, nil)
	if err != nil {
		t.Fatalf("spawn the helper: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, pty) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = pty.Close()
	})
	return waitHelper(t, out)
}

// TestStalePaneEnvOnOwnTerminalIsAllowed is the lockout the review found: a
// GUI terminal or a tmux server started from a pane inherits the pane's
// variables, and a tuios attach from it runs on a terminal of its own. That
// is not nested, and must attach.
func TestStalePaneEnvOnOwnTerminalIsAllowed(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	_, _, b := twoWindowSession(t, d, "stale")
	got := startHelperOnOwnTerminal(t, sp, "attach", "stale",
		"TUIOS_SESSION=stale", "TUIOS_PANE_ID="+b, "TUIOS_WINDOW_ID="+b, "TUIOS_SOCKET="+sp)
	if !strings.HasPrefix(got, "nonce:") {
		t.Errorf("an attach with a pane's variables from a terminal of its own got %q, want it attached", got)
	}
}

// TestDetachedPaneProcessWithoutTerminalIsRefused is the other side: a process
// with no terminal is placed by its environment, and one that names a pane of
// the session is refused.
func TestDetachedPaneProcessWithoutTerminalIsRefused(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	_, _, b := twoWindowSession(t, d, "orphan")
	out := filepath.Join(t.TempDir(), "out")
	// sh starts the helper in the background and exits, and setsid leaves it
	// with no controlling terminal.
	cmd := exec.Command("/bin/sh", "-c", helperCommand(t, sp, out, "attach", "orphan")+" &")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "TUIOS_PANE_ID="+b, helperDetachEnv+"=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("sh: %v", err)
	}
	if got := waitHelper(t, out); !strings.Contains(got, `You are inside session "orphan"`) {
		t.Errorf("a process with no terminal and a pane's id got %q, want the refusal", got)
	}
}

// addSizedClient registers a TUI client of the given size on a session, for
// the size calculation alone.
func addSizedClient(d *Daemon, id, sessionID string, w, h int) *connState {
	cs := &connState{clientID: id, sessionID: sessionID, isTUIClient: true, width: w, height: h}
	d.clientsMu.Lock()
	d.clients[id] = cs
	d.clientsMu.Unlock()
	return cs
}

// TestEffectiveSizeHasAFloor pins the floor on one client's size.
func TestEffectiveSizeHasAFloor(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test"})
	addSizedClient(d, "big", "s", 120, 40)
	addSizedClient(d, "tiny", "s", 1, 1)
	if w, h := d.calculateEffectiveSize("s"); w != 20 || h != 6 {
		t.Errorf("effective size %dx%d, want the floor 20x6", w, h)
	}
	d.clientsMu.Lock()
	delete(d.clients, "tiny")
	d.clientsMu.Unlock()
	if w, h := d.calculateEffectiveSize("s"); w != 120 || h != 40 {
		t.Errorf("effective size %dx%d with one 120x40 client, want 120x40", w, h)
	}
}

// TestNestedResizeLoopSettles runs the loop from #235 against the size
// calculation: one client at 120x40, and a nested client whose terminal is a
// pane of the session, so it is the session's size less the chrome. The loop
// must stop at a fixed point no smaller than the floor, in a few rounds.
func TestNestedResizeLoopSettles(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test"})
	addSizedClient(d, "outer", "s", 120, 40)
	nested := addSizedClient(d, "nested", "s", 118, 36)

	// Borders take two columns, and the borders and the dock take four rows.
	const chromeW, chromeH = 2, 4
	w, h := d.calculateEffectiveSize("s")
	settled := false
	for range 200 {
		nested.width, nested.height = max(w-chromeW, 1), max(h-chromeH, 1)
		nw, nh := d.calculateEffectiveSize("s")
		if nw == w && nh == h {
			settled = true
			break
		}
		w, h = nw, nh
	}
	if !settled {
		t.Fatalf("the size never settled; last %dx%d", w, h)
	}
	if w < 20 || h < 6 {
		t.Errorf("the session settled at %dx%d, below the floor 20x6", w, h)
	}
}
