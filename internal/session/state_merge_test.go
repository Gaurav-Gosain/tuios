package session

import "testing"

// clientSnapshot is what an attached TUI pushes: whatever it last saw from the
// daemon, stamped with the version it saw it at.
func clientSnapshot(sess *Session) *SessionState {
	state := sess.GetState()
	state.BaseVersion = state.Version
	state.Version = 0
	return state
}

func windowByID(t *testing.T, state *SessionState, id string) *WindowState {
	t.Helper()
	for i := range state.Windows {
		if state.Windows[i].ID == id {
			return &state.Windows[i]
		}
	}
	return nil
}

// TestStaleClientSyncKeepsDaemonCreatedWindow is the test the clobbering
// architecture could not pass: the daemon creates a window headless, a client
// that has not seen it pushes a snapshot built beforehand, and the window has to
// survive. Before state versioning the push replaced the whole state and the
// window vanished, which is why every mutating verb was routed to the TUI
// instead of being executed by the owner of the state.
func TestStaleClientSyncKeepsDaemonCreatedWindow(t *testing.T) {
	sess, err := NewSession("stale", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	// The client's view, taken before the daemon does anything.
	stale := clientSnapshot(sess)

	win, err := sess.AddDaemonWindow("headless", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	if accepted := sess.UpdateState(stale); accepted {
		t.Error("a sync built before a daemon mutation was accepted as current")
	}

	got := sess.GetState()
	if windowByID(t, got, win.ID) == nil {
		t.Fatalf("daemon-created window %s was lost to a stale client sync (windows: %+v)", win.ID, got.Windows)
	}
	if got.FocusedWindowID != win.ID {
		t.Errorf("FocusedWindowID = %q, want the daemon-focused window %q", got.FocusedWindowID, win.ID)
	}
}

// TestStaleClientSyncKeepsDaemonRename covers the per-window daemon-owned
// fields: a rename, a workspace move and a minimize performed daemon-side must
// all survive a client push that still carries the old values.
func TestStaleClientSyncKeepsDaemonMetadata(t *testing.T) {
	sess, err := NewSession("meta", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	win, err := sess.AddDaemonWindow("shell", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	// The client sees the window, then the daemon changes it underneath.
	stale := clientSnapshot(sess)
	if err := sess.RenameDaemonWindow(win.ID, "build"); err != nil {
		t.Fatalf("RenameDaemonWindow: %v", err)
	}
	if err := sess.MoveDaemonWindowToWorkspace(win.ID, 3); err != nil {
		t.Fatalf("MoveDaemonWindowToWorkspace: %v", err)
	}
	if err := sess.SetDaemonWindowMinimized(win.ID, true); err != nil {
		t.Fatalf("SetDaemonWindowMinimized: %v", err)
	}

	// The stale push carries the client's own geometry, which it does own.
	stale.Windows[0].X, stale.Windows[0].Y = 7, 9

	if accepted := sess.UpdateState(stale); accepted {
		t.Error("stale sync reported as accepted")
	}

	got := windowByID(t, sess.GetState(), win.ID)
	if got == nil {
		t.Fatal("window disappeared")
	}
	if got.CustomName != "build" {
		t.Errorf("CustomName = %q, want the daemon's rename %q", got.CustomName, "build")
	}
	if got.Workspace != 3 {
		t.Errorf("Workspace = %d, want the daemon's move to 3", got.Workspace)
	}
	if !got.Minimized {
		t.Error("Minimized was undone by a stale client sync")
	}
	if got.X != 7 || got.Y != 9 {
		t.Errorf("geometry = (%d,%d), want the client's (7,9): the client owns pixel geometry", got.X, got.Y)
	}
}

// TestStaleClientSyncKeepsClientClose pins the other side of the membership
// rule. A window the client closed must stay closed even while the client is
// stale, and closing a window closes its PTY first, which is how a close is told
// apart from a window the daemon created that the client has not seen yet.
func TestStaleClientSyncKeepsClientClose(t *testing.T) {
	sess, err := NewSession("closed", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	doomed, err := sess.AddDaemonWindow("doomed", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	other, err := sess.AddDaemonWindow("keeper", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	// The client closes the doomed window: its PTY goes first, then the sync.
	stale := clientSnapshot(sess)
	if err := sess.ClosePTY(doomed.PTYID); err != nil {
		t.Fatalf("ClosePTY: %v", err)
	}
	stale.Windows = stale.Windows[:0]
	for _, w := range sess.GetState().Windows {
		if w.ID != doomed.ID {
			stale.Windows = append(stale.Windows, w)
		}
	}
	// Meanwhile the daemon renames the surviving window, making the push stale.
	if err := sess.RenameDaemonWindow(other.ID, "kept"); err != nil {
		t.Fatalf("RenameDaemonWindow: %v", err)
	}

	sess.UpdateState(stale)

	got := sess.GetState()
	if windowByID(t, got, doomed.ID) != nil {
		t.Error("a window the client closed came back through reconciliation")
	}
	if w := windowByID(t, got, other.ID); w == nil || w.CustomName != "kept" {
		t.Errorf("surviving window = %+v, want CustomName %q", w, "kept")
	}
}

// TestCurrentClientSyncIsTakenAsSent guards the common case: a client that has
// seen everything the daemon did is authoritative for the fields it owns, and
// its push applies unchanged. This is the interactive path, so it must keep the
// pre-versioning behavior exactly.
func TestCurrentClientSyncIsTakenAsSent(t *testing.T) {
	sess, err := NewSession("current", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	win, err := sess.AddDaemonWindow("shell", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	// The client has seen the creation, so its own rename wins.
	sync := clientSnapshot(sess)
	sync.Windows[0].CustomName = "client-named"
	sync.CurrentWorkspace = 4

	if accepted := sess.UpdateState(sync); !accepted {
		t.Error("an up-to-date client sync was reported as stale")
	}

	got := sess.GetState()
	if w := windowByID(t, got, win.ID); w == nil || w.CustomName != "client-named" {
		t.Errorf("window = %+v, want the client's rename to apply", w)
	}
	if got.CurrentWorkspace != 4 {
		t.Errorf("CurrentWorkspace = %d, want the client's 4", got.CurrentWorkspace)
	}
}

// TestUnversionedClientSyncIsTakenAtFaceValue keeps older clients working. A
// client that predates state versioning sends no BaseVersion and cannot say what
// it saw, so its syncs behave exactly as they did before: applied as sent.
func TestUnversionedClientSyncIsTakenAtFaceValue(t *testing.T) {
	sess, err := NewSession("legacy", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	legacy := sess.GetState()
	legacy.Version, legacy.BaseVersion = 0, 0
	legacy.Windows = nil
	legacy.FocusedWindowID = ""

	if accepted := sess.UpdateState(legacy); !accepted {
		t.Error("an unversioned client sync was reported as stale")
	}
	if got := sess.GetState(); len(got.Windows) != 0 {
		t.Errorf("windows = %d, want the unversioned push to apply as sent", len(got.Windows))
	}
}

// TestClientSyncDoesNotAdvanceVersion pins what the version counts. It counts
// daemon-side mutations, so a client that syncs and immediately syncs again is
// still current; if a sync advanced it, every client would be permanently one
// version behind and every push would be reconciled.
func TestClientSyncDoesNotAdvanceVersion(t *testing.T) {
	sess, err := NewSession("versions", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	v := sess.GetState().Version
	if v == 0 {
		t.Fatal("a daemon-side window creation did not advance the state version")
	}

	for i := range 3 {
		if accepted := sess.UpdateState(clientSnapshot(sess)); !accepted {
			t.Fatalf("sync %d reported as stale", i)
		}
		if got := sess.GetState().Version; got != v {
			t.Fatalf("version = %d after client sync %d, want it unchanged at %d", got, i, v)
		}
	}
}

// TestClientSyncKeepsDaemonExclusiveFields covers the fields no client ever
// sets. A sync that simply omits them must not wipe them.
func TestClientSyncKeepsDaemonExclusiveFields(t *testing.T) {
	sess, err := NewSession("exclusive", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	win, err := sess.AddDaemonWindow("shell", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	sess.SetOption("theme", "nord")

	// Stamp a cwd the way the resurrection capture does, then push a client
	// snapshot that carries neither options nor cwd.
	seeded := sess.GetState()
	seeded.Windows[0].Cwd = "/home/user/project"
	seeded.ResurrectionVersion = 2
	sess.UpdateState(seeded)

	sync := clientSnapshot(sess)
	sync.Options = nil
	sync.ResurrectionVersion = 0
	for i := range sync.Windows {
		sync.Windows[i].Cwd = ""
	}
	sess.UpdateState(sync)

	got := sess.GetState()
	if got.Options["theme"] != "nord" {
		t.Errorf("Options = %v, want the daemon-owned theme to survive", got.Options)
	}
	if got.ResurrectionVersion != 2 {
		t.Errorf("ResurrectionVersion = %d, want 2", got.ResurrectionVersion)
	}
	if w := windowByID(t, got, win.ID); w == nil || w.Cwd != "/home/user/project" {
		t.Errorf("window = %+v, want the daemon-captured cwd to survive", w)
	}
}

// TestStaleClientSyncCannotResurrectAClosedWindow is the counterpart to
// TestStaleClientSyncKeepsDaemonCreatedWindow, and it is the bug the converged
// close ran into first. Pressing the close chord sends a CloseWindow intent and
// then, as every keystroke does, pushes the client's state. That push was built
// before the close landed, so it still lists the window. Treating an unknown
// window as one the client had just created put it straight back, and the window
// never closed.
//
// Clients do not create windows any more, so an incoming window the daemon does
// not know is never news. It is always a snapshot from before a close.
func TestStaleClientSyncCannotResurrectAClosedWindow(t *testing.T) {
	sess, err := NewSession("resurrect", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	keeper, err := sess.AddDaemonWindow("keeper", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	doomed, err := sess.AddDaemonWindow("doomed", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	// The client's snapshot, taken before the close.
	stale := clientSnapshot(sess)

	if _, err := sess.CloseDaemonWindow(doomed.ID); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}

	// The keystroke's own state push arrives afterwards, still listing both.
	sess.UpdateState(stale)

	got := sess.GetState()
	if windowByID(t, got, doomed.ID) != nil {
		t.Error("the closed window came back through the client's own state push")
	}
	if windowByID(t, got, keeper.ID) == nil {
		t.Error("reconciliation dropped a window the daemon still holds")
	}
	if len(got.Windows) != 1 {
		t.Errorf("state holds %d windows, want 1", len(got.Windows))
	}
}
