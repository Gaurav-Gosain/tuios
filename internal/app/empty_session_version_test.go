package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAPushFromAnEmptyAttachKeepsADaemonCreatedWindow is the window a script
// opened with tuios new-window vanishing from a session a client had just
// attached to while it was empty.
//
// A client echoes the daemon state version it last saw on every push, and the
// daemon reads 0 as a client too old to say, whose push it takes as sent. A
// client attached to an empty session never recorded the version, so its first
// push said 0 and replaced the session's window list with its own empty one.
// The e2e test TestTheFileSectionListsARemotePanesFiles lost its far window
// this way about one run in forty on a loaded Linux runner.
//
// NEGATIVE CONTROL: with adoptEmptySessionVersion removed from
// RestoreAttachedSession the push is taken as sent and the window is gone.
func TestAPushFromAnEmptyAttachKeepsADaemonCreatedWindow(t *testing.T) {
	ownSocket(t)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	const name = "empty"
	c := session.NewTUIClient()
	if err := c.Connect("test", rigCols, rigRows); err != nil {
		t.Fatalf("connect: %v", err)
	}
	// An attach that creates the session hands it back with no windows.
	state, err := c.AttachSession(name, true, rigCols, rigRows)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if state != nil && len(state.Windows) != 0 {
		t.Fatalf("the new session came with %d windows, want none", len(state.Windows))
	}
	c.StartReadLoop()
	t.Cleanup(func() { _ = c.Close() })

	m := NewOS(OSOptions{
		UserConfig:      config.DefaultConfig(),
		IsDaemonSession: true,
		DaemonClient:    c,
		SessionName:     c.SessionName(),
		Width:           rigCols,
		Height:          rigRows,
	})
	m.RestoreAttachedSession(state)

	// A window the daemon makes on its own account, the way a verb does. The
	// state sync it causes reaches the client's read loop, and nothing applies
	// it: this client's next push is built from the empty session it attached
	// to, which is the push that raced the sync in the e2e test.
	ctl := session.NewTUIClient()
	if err := ctl.Connect("test", rigCols, rigRows); err != nil {
		t.Fatalf("control connect: %v", err)
	}
	if _, err := ctl.AttachSession(name, false, rigCols, rigRows); err != nil {
		t.Fatalf("control attach: %v", err)
	}
	ctl.StartReadLoop()
	t.Cleanup(func() { _ = ctl.Close() })
	if err := ctl.SendIntent("NewWindow"); err != nil {
		t.Fatalf("new window: %v", err)
	}
	windows := func(cl *session.TUIClient) int {
		list, err := cl.RefreshSessionList()
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		for _, s := range list {
			if s.Name == name {
				return s.WindowCount
			}
		}
		t.Fatalf("session %q is not listed", name)
		return 0
	}
	rigWaitUntil(t, "the daemon to create the window", func() bool { return windows(ctl) == 1 })

	m.SyncStateToDaemon()
	// Asked on the connection the push went out on, which the daemon reads in
	// order, so the answer comes after the push has been applied.
	if n := windows(c); n != 1 {
		t.Fatalf("a push built before the client saw the new window left the session with %d windows, want 1: "+
			"the client pushed base version %d", n, m.DaemonStateVersion)
	}
}

// TestSwitchingToAnEmptySessionAdoptsItsVersion is the other way a client came
// to hold no version for the session it was on: a switch into an empty session
// kept the version of the session it left.
func TestSwitchingToAnEmptySessionAdoptsItsVersion(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	m.DaemonStateVersion = 41
	m.rebuildForSession(&session.SessionState{Name: "empty", Version: 3}, rigCols, rigRows)
	if m.DaemonStateVersion != 3 {
		t.Fatalf("after a switch to an empty session at version 3 the client holds version %d", m.DaemonStateVersion)
	}
}
