//go:build linux || darwin

package session

import (
	"path/filepath"
	"strings"
	"testing"
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
