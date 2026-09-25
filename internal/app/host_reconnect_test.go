package app

import (
	"errors"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The policy for getting a lost link back, pinned where it can be read.
//
// The behaviour these hold is the one the maintainer asked for: a link that
// drops is dialed again without him asking, the pane he was working in stays on
// screen while that happens, and a failure that trying again cannot fix stops
// at once with the reason rather than being retried until the budget runs out.

// TestASwitchTheUserAskedForEndsTheReconnect is the "a reconnect must not
// resurrect a session the person left" rule. Detaching on purpose and losing a
// link are different events, and a dial that lands after the user has moved on
// must not pull them back.
func TestASwitchTheUserAskedForEndsTheReconnect(t *testing.T) {
	win := newTestWindow(t, "recon-0002", 40, 10)
	m := newTestOS(win)
	m.Settings = config.Global
	m.AttachedHost = "oci"
	m.SessionName = "work"
	_ = m.beginHostReconnect(errors.New("the pipe closed"))
	stale := m.hostReconnect.gen

	// The shipped path a deliberate switch runs through.
	m.WorkspaceTrees = map[int]*layout.BSPTree{}
	m.WorkspaceScrollingLayouts = map[int]*layout.ScrollingLayout{}
	m.WindowToBSPID = map[string]int{}
	m.BSPIDToWindowID = map[int]string{}
	m.SubscribedPTYs = map[string]bool{}
	m.Windows = nil
	m.adoptClient(session.NewTUIClient(), nil, "local")

	if m.ReconnectingToHost() {
		t.Error("ASSERTION: a session the user chose still has a reconnect running against the one they left")
	}
	// A dial that finishes now belongs to nobody and must be dropped.
	if cmd := m.handleHostReconnectResult(hostReconnectResultMsg{gen: stale}); cmd != nil {
		t.Error("ASSERTION: a dial from the abandoned attempt was applied to the session the user switched to")
	}
	if cmd := m.handleHostReconnectTick(hostReconnectTickMsg{gen: stale}); cmd != nil {
		t.Error("ASSERTION: the abandoned attempt kept dialing after the user switched away")
	}
}

var _ = terminal.Window{}
