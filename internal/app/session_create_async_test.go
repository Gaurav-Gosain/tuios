package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSidebarNewSessionDoesNotBlockUpdate pins the fix for a real freeze: the
// creation is a daemon round trip, and running it inline on the Update goroutine
// parked input, rendering and socket draining for as long as the daemon took.
// The call must return at once and deliver its result as a message instead.
//
// The daemon-backed half (that a session really appears) is covered by the e2e
// suite; what cannot regress silently is this call blocking again.
func TestSidebarNewSessionDoesNotBlockUpdate(t *testing.T) {
	m := &OS{}

	// No daemon: the guard refuses and must still return immediately.
	done := make(chan struct{})
	go func() {
		m.SidebarNewSession()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SidebarNewSession blocked the caller")
	}
	if len(m.Notifications) == 0 {
		t.Fatal("refusing without a daemon should say why")
	}
}

// TestSessionCreateChanIsLazyAndStable keeps the channel from being rebuilt per
// call, which would strand the listener armed on the previous one and lose the
// result.
func TestSessionCreateChanIsLazyAndStable(t *testing.T) {
	m := &OS{}
	if m.PendingSessionCreate != nil {
		t.Fatal("a client that never creates a session should not allocate the channel")
	}
	first := m.sessionCreateChan()
	if first == nil {
		t.Fatal("sessionCreateChan returned nil")
	}
	if second := m.sessionCreateChan(); second != first {
		t.Fatal("sessionCreateChan rebuilt the channel; the armed listener would miss results")
	}
	// Buffered, so the creating goroutine cannot leak waiting on a full channel
	// if the listener is momentarily elsewhere.
	if cap(first) == 0 {
		t.Fatal("the channel must be buffered so the creating goroutine cannot leak")
	}
}

// TestSidebarFocusResolvesByID pins the fix for a wrong-pane hazard: rail hit
// rects carry the index a row was drawn with, and a pane closing before the
// click shifts every later index. Focusing by that stale index selected a
// different pane than the row named, and the context menu built on top of it
// then offered to close that one.
func TestSidebarFocusResolvesByID(t *testing.T) {
	a := &terminal.Window{ID: "a"}
	b := &terminal.Window{ID: "b"}
	c := &terminal.Window{ID: "c"}
	m := &OS{Windows: []*terminal.Window{a, b, c}, FocusedWindow: 0}

	// The rail drew B at index 1. A exits before the click lands.
	hit := sidebarRowHit{Kind: sidebarRowWindow, WindowID: "b", WindowIndex: 1}
	m.Windows = []*terminal.Window{b, c}

	m.sidebarFocusWindow(hit)

	if got := m.Windows[m.FocusedWindow].ID; got != "b" {
		t.Fatalf("focused %q, want the pane the row named (b); the stale index would give %q",
			got, m.Windows[1].ID)
	}
}
