package session

import (
	"bytes"
	"encoding/gob"
	"maps"
	"testing"
)

// TestTheCustomLayoutMapSurvivesTheWire covers the field's own encoding, and the
// question a map field on a gob struct always raises: what an entry-less one
// arrives as.
//
// Measured rather than assumed. gob skips a struct field whose value is the zero
// value, and reflect's IsZero for a map is IsNil, so a nil map is dropped and an
// entry-less non-nil map is sent and arrives entry-less and non-nil. Both
// therefore reach a reader, and every reader of this field treats them alike:
// nil and empty both mean the sender said nothing about any workspace.
// BuildSessionState nils an entry-less map before sending so the two cases stay
// one case on the wire as well.
//
// The false entry has its own case. It is the zero value of the map's element
// type, not of the map, so gob carries it, and a present false is how a client
// says a workspace stopped being custom. A wire format that dropped it would
// turn that into silence, which the merges read as "keep what you have".
//
// NEGATIVE CONTROL: assert the empty map arrives as nil, and the case fails on
// the empty map it actually gets.
func TestTheCustomLayoutMapSurvivesTheWire(t *testing.T) {
	roundTrip := func(t *testing.T, in *SessionState) *SessionState {
		t.Helper()
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(in); err != nil {
			t.Fatalf("gob encode: %v", err)
		}
		var out SessionState
		if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
			t.Fatalf("gob decode: %v", err)
		}
		return &out
	}

	t.Run("populated, false entries included", func(t *testing.T) {
		out := roundTrip(t, &SessionState{
			Name:               "wire",
			CurrentWorkspace:   3,
			WorkspaceHasCustom: map[int]bool{1: false, 3: true, 9: false},
		})
		want := map[int]bool{1: false, 3: true, 9: false}
		if !maps.Equal(out.WorkspaceHasCustom, want) {
			t.Errorf("came back as %v, want %v: a present false is a client clearing a "+
				"workspace and must not be dropped", out.WorkspaceHasCustom, want)
		}
	})

	t.Run("entry-less map stays entry-less", func(t *testing.T) {
		out := roundTrip(t, &SessionState{
			Name:               "wire",
			CurrentWorkspace:   1,
			WorkspaceHasCustom: map[int]bool{},
		})
		if len(out.WorkspaceHasCustom) != 0 {
			t.Errorf("an entry-less map came back as %#v, want no entries", out.WorkspaceHasCustom)
		}
		if out.WorkspaceHasCustom == nil {
			t.Errorf("an entry-less map came back nil; gob sends it, and this case is the record " +
				"of that, not of a wish. Both forms are read alike, so the change is harmless, " +
				"but the comment above is then wrong and should be rewritten.")
		}
	})

	t.Run("nil map is dropped", func(t *testing.T) {
		out := roundTrip(t, &SessionState{Name: "wire", CurrentWorkspace: 1, WorkspaceHasCustom: nil})
		if out.WorkspaceHasCustom != nil {
			t.Errorf("a nil map came back as %#v, want nil", out.WorkspaceHasCustom)
		}
	})

	t.Run("an older peer says nothing and the rest still reads", func(t *testing.T) {
		out := roundTrip(t, &SessionState{Name: "wire", CurrentWorkspace: 4, AutoTiling: true})
		if out.WorkspaceHasCustom != nil {
			t.Errorf("WorkspaceHasCustom = %v, want nil", out.WorkspaceHasCustom)
		}
		if !out.AutoTiling || out.CurrentWorkspace != 4 {
			t.Errorf("the fields an older peer does send came back as tiling=%v on workspace %d, "+
				"want true on 4", out.AutoTiling, out.CurrentWorkspace)
		}
	})
}

// TestAPushUnionsTheCustomFlagsItDoesNotKnowAbout is the merge path. A client
// only ever holds a flag for a workspace it has been told about or arranged
// itself, so letting a push replace the set would drop every workspace that
// client never heard of - which is the bug this field exists to stop, one layer
// further down.
//
// NEGATIVE CONTROL: replace the union in retainDaemonExclusive with a plain nil
// check and workspace 3's flag vanishes from the session.
func TestAPushUnionsTheCustomFlagsItDoesNotKnowAbout(t *testing.T) {
	canonical := &SessionState{
		Name:               "s",
		CurrentWorkspace:   3,
		WorkspaceHasCustom: map[int]bool{1: false, 3: true},
		Version:            1,
	}
	incoming := &SessionState{
		Name:               "s",
		CurrentWorkspace:   1,
		WorkspaceHasCustom: map[int]bool{1: true},
		Version:            1,
	}

	retainDaemonExclusive(incoming, canonical)

	want := map[int]bool{1: true, 3: true}
	if !maps.Equal(incoming.WorkspaceHasCustom, want) {
		t.Errorf("merged to %v, want %v: the pushing client's own flag stands and the workspace "+
			"it never visited keeps the layout another client arranged", incoming.WorkspaceHasCustom, want)
	}
}

// TestAPushCanStillClearACustomFlag is the direction the union must not block. A
// client that moved a pane off a workspace clears the flag there and says so
// with a present false, which has to beat the session's true.
//
// NEGATIVE CONTROL: make the union prefer canonical where both hold an entry and
// workspace 3 stays custom for good.
func TestAPushCanStillClearACustomFlag(t *testing.T) {
	canonical := &SessionState{WorkspaceHasCustom: map[int]bool{3: true}}
	incoming := &SessionState{WorkspaceHasCustom: map[int]bool{3: false}}

	retainDaemonExclusive(incoming, canonical)

	if incoming.WorkspaceHasCustom[3] {
		t.Errorf("workspace 3 is still custom after a client said it is not")
	}
}

// TestAPeerThatSaysNothingKeepsTheSessionsCustomFlags: a nil map on the wire is
// an older peer, or a client with tiling off, or (gob being gob) a client with
// nothing to say. None of those is an instruction to forget.
//
// NEGATIVE CONTROL: drop the union block and the session's flags are gone.
func TestAPeerThatSaysNothingKeepsTheSessionsCustomFlags(t *testing.T) {
	canonical := &SessionState{WorkspaceHasCustom: map[int]bool{2: true}}
	incoming := &SessionState{}

	retainDaemonExclusive(incoming, canonical)

	if !incoming.WorkspaceHasCustom[2] {
		t.Errorf("workspace 2 lost its custom flag to a push that said nothing")
	}
}

// TestAStaleClientCannotDropACustomFlagItNeverSaw drives the whole daemon-side
// path: a client pushes a snapshot built before a daemon mutation, so UpdateState
// runs reconcileStale over it as well as the union.
//
// NEGATIVE CONTROL: drop the union block from retainDaemonExclusive and the
// session loses workspace 3's flag to a stale push.
func TestAStaleClientCannotDropACustomFlagItNeverSaw(t *testing.T) {
	sess, err := NewSession("custom", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)

	// Client A settles the session with workspace 3 arranged by hand.
	sess.UpdateState(&SessionState{
		Name:               "custom",
		CurrentWorkspace:   3,
		AutoTiling:         true,
		WorkspaceHasCustom: map[int]bool{3: true},
		BaseVersion:        1,
	})
	if !sess.GetState().WorkspaceHasCustom[3] {
		t.Fatalf("the session does not hold workspace 3 as custom after A's push")
	}

	// A daemon-side mutation moves the version on, so B's next push is stale.
	if err := sess.SetDisplayName("Custom"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}

	// Client B has never been to workspace 3 and pushes a snapshot from before
	// the rename.
	accepted := sess.UpdateState(&SessionState{
		Name:               "custom",
		CurrentWorkspace:   1,
		AutoTiling:         true,
		WorkspaceHasCustom: map[int]bool{1: false},
		BaseVersion:        1,
	})
	if accepted {
		t.Fatalf("the stale push was accepted as sent; the case is not exercising reconcileStale")
	}

	final := sess.GetState()
	if !final.WorkspaceHasCustom[3] {
		t.Errorf("workspace 3 lost its custom flag to B's push: B destroyed a layout it never touched")
	}
	if final.WorkspaceHasCustom[1] {
		t.Errorf("workspace 1 is custom, want the false B pushed")
	}
}

// TestTheStateSnapshotDoesNotShareItsCustomFlagMap: GetState hands a copy out to
// every caller, and a shared map would let one of them write into the canonical
// state without the lock.
//
// NEGATIVE CONTROL: remove the clone from snapshotStateLocked and the write
// through the snapshot reaches the session.
func TestTheStateSnapshotDoesNotShareItsCustomFlagMap(t *testing.T) {
	sess, err := NewSession("snapcustom", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	sess.UpdateState(&SessionState{
		Name:               "snapcustom",
		CurrentWorkspace:   1,
		WorkspaceHasCustom: map[int]bool{1: true},
		BaseVersion:        1,
	})

	snap := sess.GetState()
	snap.WorkspaceHasCustom[1] = false

	if !sess.GetState().WorkspaceHasCustom[1] {
		t.Errorf("the session lost workspace 1's flag to a write through a snapshot")
	}
}

// TestARestoredSessionComesBackWithItsCustomFlags is the resurrection path. The
// daemon rebuilds the session from the state on disk and works on a copy of it,
// and every map it means to keep has to be cloned onto that copy by name. A flag
// left behind there is the same loss as one left off the wire: every workspace
// comes back as the tiler's and the first switch retiles the layouts away.
//
// NEGATIVE CONTROL: drop the WorkspaceHasCustom clone from restoreSession and
// the session comes back with no flags at all.
func TestARestoredSessionComesBackWithItsCustomFlags(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()

	cwd := t.TempDir()
	sess, err := d.restoreSession(&SessionState{
		Name:               "resurrected",
		Width:              80,
		Height:             24,
		CurrentWorkspace:   1,
		AutoTiling:         true,
		WorkspaceHasCustom: map[int]bool{2: true, 3: false},
		Windows: []WindowState{
			{ID: "win-1", Title: "shell", X: 0, Y: 0, Width: 60, Height: 24, Workspace: 2, PTYID: "dead-pty", Cwd: cwd},
		},
	})
	if err != nil {
		t.Fatalf("restoreSession: %v", err)
	}

	got := sess.GetState().WorkspaceHasCustom
	want := map[int]bool{2: true, 3: false}
	if !maps.Equal(got, want) {
		t.Errorf("the restored session holds %v, want %v", got, want)
	}
}

// TestAStalePushCanStillMarkAWorkspaceCustom is why reconcileStale does not take
// this field from canonical.
//
// The daemon never marks a layout custom and never clears one, so canonical is
// not newer here by construction the way it is for the focus. A client that
// arranged a layout and pushed a snapshot from before some unrelated daemon
// mutation is still the only thing that knows, and taking canonical's answer
// would throw that away every time the two crossed.
//
// NEGATIVE CONTROL: add WorkspaceHasCustom to the fields reconcileStale takes
// from canonical and workspace 5 comes back not custom.
func TestAStalePushCanStillMarkAWorkspaceCustom(t *testing.T) {
	sess, err := NewSession("stalecustom", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)

	sess.UpdateState(&SessionState{
		Name:               "stalecustom",
		CurrentWorkspace:   5,
		AutoTiling:         true,
		WorkspaceHasCustom: map[int]bool{5: false},
		BaseVersion:        1,
	})
	if err := sess.SetDisplayName("Stale"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}

	accepted := sess.UpdateState(&SessionState{
		Name:               "stalecustom",
		CurrentWorkspace:   5,
		AutoTiling:         true,
		WorkspaceHasCustom: map[int]bool{5: true},
		BaseVersion:        1,
	})
	if accepted {
		t.Fatalf("the push was accepted as sent; the case is not exercising reconcileStale")
	}
	if !sess.GetState().WorkspaceHasCustom[5] {
		t.Errorf("workspace 5 is not custom: the resize that arrived on a stale snapshot was thrown away")
	}
}
