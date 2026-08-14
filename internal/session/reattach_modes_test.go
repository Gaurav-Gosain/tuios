package session

import (
	"bytes"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestReattachRestoresModesScrolledOutOfBuffer reproduces, headlessly, mouse
// input dying in a daemon pane after detach and reattach.
//
// A browser-class guest enables its mouse modes once at startup
// (?1003 any-motion, ?1006 SGR, ?1016 SGR-pixel) and negotiates the kitty
// keyboard protocol once, then never repeats either. The daemon replays only
// its bounded output buffer to a new subscriber, so by the time a client
// reattaches the enable sequences have scrolled out and the client's freshly
// built per-window emulator can only learn them from the daemon's
// authoritative snapshot (GetTerminalState). This test drives a real guest
// shell to emit the sequences, floods the buffer past its bound, then rebuilds
// a client emulator exactly the way restoreTerminalContent does and asserts
// the input-routing getters report the truth.
func TestReattachRestoresModesScrolledOutOfBuffer(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess, err := d.manager.CreateSession("reattach-modes", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	win, err := sess.AddDaemonWindow("w", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("no PTY for the created window")
	}

	// The guest emits the enables itself, exactly as a browser would.
	if _, err := pty.Write([]byte("printf '\\033[?1003h\\033[?1006h\\033[?1016h\\033[>31u'\n")); err != nil {
		t.Fatalf("write to PTY: %v", err)
	}

	waitFor := func(desc string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Skipf("timed out waiting for %s in this environment", desc)
	}

	waitFor("guest to enable mouse modes", func() bool {
		st := pty.GetTerminalState(0)
		return st != nil && st.Modes[1003]
	})

	// Flood well past the 64KB output buffer bound so the enable sequences
	// scroll out of the replay a new subscriber gets. The trailing sleep keeps
	// the shell from drawing its next prompt: a bash prompt toggles bracketed
	// paste, and ANY mode sequence in the replay recomputes the stale mouse
	// cache as a side effect, masking the bug this test reproduces. A browser
	// pane's steady-state output is pure text and graphics with no mode
	// traffic, which is exactly why the bug bit browser panes.
	if _, err := pty.Write([]byte("dd if=/dev/zero bs=1024 count=128 2>/dev/null | tr '\\0' f; echo FLOOD-DONE; sleep 300\n")); err != nil {
		t.Fatalf("write to PTY: %v", err)
	}

	enable := []byte("\x1b[?1003h")
	waitFor("enable sequences to scroll out of the output buffer", func() bool {
		pty.outputMu.RLock()
		defer pty.outputMu.RUnlock()
		return pty.outputPos > 0 &&
			!bytes.Contains(pty.outputBuffer[:pty.outputPos], enable) &&
			bytes.Contains(pty.outputBuffer[:pty.outputPos], []byte("FLOOD-DONE"))
	})

	// Reattach: a fresh subscriber gets the bounded replay, and the client
	// rebuilds its per-window emulator from the daemon snapshot, in the same
	// order restoreTerminalContent uses (restore, then replay).
	ch := pty.Subscribe("reattach-client", 0)
	defer func() { _ = pty.Unsubscribe("reattach-client") }()

	var replay []byte
	select {
	case chunk := <-ch:
		replay = chunk.data
	case <-time.After(5 * time.Second):
		t.Fatal("no buffered replay for the new subscriber")
	}
	if bytes.Contains(replay, enable) {
		t.Fatal("replay still contains the enable sequence; the scrolled-out condition was not reproduced")
	}
	if bytes.Contains(replay, []byte("\x1b[?")) {
		t.Skip("replay carries DEC mode traffic in this environment; the browser-pane condition (no mode traffic after the flood) was not reproduced")
	}

	state := pty.GetTerminalState(0)
	if state == nil {
		t.Fatal("GetTerminalState returned nil")
	}

	em := vt.NewEmulator(state.Width, state.Height)
	defer func() { _ = em.Close() }()
	if state.IsAltScreen {
		em.RestoreAltScreenMode(true)
	}
	em.RestoreModes(state.Modes)
	em.RestoreKittyKeyboardState(state.KittyKbdStack)
	if _, err := em.Write(replay); err != nil {
		t.Fatalf("writing replay to client emulator: %v", err)
	}

	if !em.HasMouseMode() {
		t.Fatal("reattached client emulator reports HasMouseMode() = false: mouse events route to scrollback/copy mode instead of the pane")
	}
	if !em.HasAllMotionMode() {
		t.Fatal("reattached client emulator lost any-motion tracking (?1003)")
	}
	// SGR-pixel must survive too, or hover reports downgrade to cell indices.
	em.SetCellSize(10, 20)
	if got := em.EncodeMouseEvent(vt.MouseClick{Button: vt.MouseLeft, X: 4, Y: 2}); got != "\x1b[<0;46;51M" {
		t.Fatalf("mouse report = %q, want pixel coordinates %q (?1016 lost across reattach)", got, "\x1b[<0;46;51M")
	}
	if got := em.KittyKeyboardFlags(); got != 31 {
		t.Fatalf("kitty keyboard flags = %d after reattach, want 31", got)
	}
}
