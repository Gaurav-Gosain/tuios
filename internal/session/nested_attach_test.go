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

// spawnHelperOnOwnTerminal starts the helper from this test process, outside
// every pane, on a PTY of its own, with no TUIOS_ variables but env. It
// returns the PTY, whose output is what the helper wrote to its terminal.
func spawnHelperOnOwnTerminal(t *testing.T, sp, out, mode, args string, env ...string) io.Reader {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	pty, cmd, err := ptyspawn.Spawn(80, 24, func() *exec.Cmd {
		c := exec.Command(exe, "-test.run=^TestHelperSocketCaller$")
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "TUIOS_") {
				c.Env = append(c.Env, kv)
			}
		}
		c.Env = append(c.Env, helperSockEnv+"="+sp, helperOutEnv+"="+out,
			helperModeEnv+"="+mode, helperArgsEnv+"="+args)
		c.Env = append(c.Env, env...)
		return c
	}, nil)
	if err != nil {
		t.Fatalf("spawn the helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = pty.Close()
	})
	return pty
}

// TestTerminalWindowFromPaneIsAllowed is a terminal window started from a
// pane of the session: the client in it has the pane's shell among its
// ancestors and the pane's variables, but a terminal of its own whose output
// does not reach the pane. script with its output thrown away stands in for
// the window. Its probe is never seen, so it attaches without --force.
func TestTerminalWindowFromPaneIsAllowed(t *testing.T) {
	skipWithoutPeerPID(t)
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "window")
	out := filepath.Join(t.TempDir(), "out")
	helper := helperCommand(t, sp, out, "attach-probe", "window")
	runInPane(t, d, sess, b, "script -qfec "+shellQuote(helper)+" /dev/null >/dev/null 2>&1")
	if got := waitHelper(t, out); !strings.HasPrefix(got, "nonce:") {
		t.Errorf("a client on its own terminal whose output does not reach the pane got %q, want it attached", got)
	}
}

// shellQuote quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// relayIntoPane copies what src writes into the given pane's output, as ssh
// or a relay would: the pane runs cat on a fifo, and src is copied into it.
//
// It returns once cat is reading, so the relay is in place before the client
// starts and writes its probe, as it is for a client started through ssh.
func relayIntoPane(t *testing.T, d *Daemon, sess *Session, pane string) io.Writer {
	t.Helper()
	fifo := filepath.Join(t.TempDir(), "relay")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	runInPane(t, d, sess, pane, "cat "+fifo)
	// Opening for write blocks until cat opens it for read.
	f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open the relay: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestProbeRelayedIntoOwnPaneIsRefused is the client no process test can
// place: outside every pane, on a terminal of its own, with no pane
// variables, whose output a relay copies into a pane of the session it asks
// for. Its probe shows up in that pane, and it is refused. The served case is
// the SSH server's client, whose probe travels down the ssh channel.
func TestProbeRelayedIntoOwnPaneIsRefused(t *testing.T) {
	skipWithoutPeerPID(t)
	for _, mode := range []string{"attach-probe", "attach-served-probe"} {
		t.Run(mode, func(t *testing.T) {
			d, sp := startTestDaemon(t)
			sess, _, b := twoWindowSession(t, d, "relay")
			out := filepath.Join(t.TempDir(), "out")
			relay := relayIntoPane(t, d, sess, b)
			pty := spawnHelperOnOwnTerminal(t, sp, out, mode, "relay")
			go func() { _, _ = io.Copy(relay, pty) }()
			if got := waitHelper(t, out); !strings.Contains(got, `You are inside session "relay"`) {
				t.Errorf("a client relayed into its own session's pane got %q, want the refusal", got)
			}
		})
	}
}

// TestProbeRelayedIntoOtherSessionIsAllowed is the control: the same relay
// into a pane of a different session is the ordinary use.
func TestProbeRelayedIntoOtherSessionIsAllowed(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "carrier")
	makeSessionWithWindow(t, d, "shown")
	out := filepath.Join(t.TempDir(), "out")
	relay := relayIntoPane(t, d, sess, b)
	pty := spawnHelperOnOwnTerminal(t, sp, out, "attach-probe", "shown")
	go func() { _, _ = io.Copy(relay, pty) }()
	if got := waitHelper(t, out); !strings.HasPrefix(got, "nonce:") {
		t.Errorf("a client relayed into another session's pane got %q, want it attached", got)
	}
}

// TestMutualNestingIsRefused is session A shown in a pane of B, then B shown
// in a pane of A: each shrinks the other. The second attach is refused.
func TestMutualNestingIsRefused(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	a, _, aPane := twoWindowSession(t, d, "mutual-a")
	b, _, bPane := twoWindowSession(t, d, "mutual-b")

	first := filepath.Join(t.TempDir(), "first")
	runInPane(t, d, a, aPane, helperCommand(t, sp, first, "attach-hold", "mutual-b"))
	if got := waitHelper(t, first); !strings.HasPrefix(got, "nonce:") {
		t.Fatalf("B in a pane of A got %q, want it attached", got)
	}

	second := filepath.Join(t.TempDir(), "second")
	runInPane(t, d, b, bPane, helperCommand(t, sp, second, "attach", "mutual-a"))
	got := waitHelper(t, second)
	if !strings.Contains(got, `is shown inside session "mutual-a"`) {
		t.Errorf("A in a pane of B, with B already in a pane of A, got %q, want the refusal", got)
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
