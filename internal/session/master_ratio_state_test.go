package session

import (
	"bytes"
	"encoding/gob"
	"maps"
	"testing"
)

// TestTheMasterRatioMapSurvivesTheWire covers the field's own encoding, and the
// question a map field on a gob struct always raises: what an entry-less one
// arrives as.
//
// Measured rather than assumed, because the answer is not the one the empty
// slice next door gets. gob skips a struct field whose value is the zero value
// (encode.go: !state.sendZero && v.IsZero()), and reflect's IsZero for a map is
// IsNil, so a nil map is dropped and an entry-less non-nil map is sent and
// arrives as an entry-less non-nil map. An empty slice, whose IsZero is also
// IsNil but which gob length-prefixes, comes back nil.
//
// Both therefore reach a reader, and every reader of this field treats them
// alike: nil and empty both mean the sender said nothing about any workspace.
// BuildSessionState nils an entry-less map before sending so the two cases stay
// one case on the wire as well.
//
// NEGATIVE CONTROL: assert the empty map arrives as nil and the case fails on
// the empty map it actually gets.
func TestTheMasterRatioMapSurvivesTheWire(t *testing.T) {
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

	t.Run("populated", func(t *testing.T) {
		out := roundTrip(t, &SessionState{
			Name:                 "wire",
			CurrentWorkspace:     3,
			MasterRatio:          0.7,
			WorkspaceMasterRatio: map[int]float64{1: 0.5, 3: 0.7, 9: 0.25},
		})
		want := map[int]float64{1: 0.5, 3: 0.7, 9: 0.25}
		if !maps.Equal(out.WorkspaceMasterRatio, want) {
			t.Errorf("came back as %v, want %v", out.WorkspaceMasterRatio, want)
		}
	})

	t.Run("entry-less map stays entry-less", func(t *testing.T) {
		out := roundTrip(t, &SessionState{
			Name:                 "wire",
			CurrentWorkspace:     1,
			MasterRatio:          0.5,
			WorkspaceMasterRatio: map[int]float64{},
		})
		if len(out.WorkspaceMasterRatio) != 0 {
			t.Errorf("an entry-less map came back as %#v, want no entries", out.WorkspaceMasterRatio)
		}
		if out.WorkspaceMasterRatio == nil {
			t.Errorf("an entry-less map came back nil; gob sends it, and this case is the record " +
				"of that, not of a wish. Both forms are read alike, so the change is harmless, " +
				"but the comment above is then wrong and should be rewritten.")
		}
	})

	t.Run("nil map is dropped", func(t *testing.T) {
		out := roundTrip(t, &SessionState{
			Name:                 "wire",
			CurrentWorkspace:     1,
			MasterRatio:          0.5,
			WorkspaceMasterRatio: nil,
		})
		if out.WorkspaceMasterRatio != nil {
			t.Errorf("a nil map came back as %#v, want nil", out.WorkspaceMasterRatio)
		}
	})

	t.Run("absent leaves the flat ratio saying it", func(t *testing.T) {
		out := roundTrip(t, &SessionState{Name: "wire", CurrentWorkspace: 4, MasterRatio: 0.65})
		if out.WorkspaceMasterRatio != nil {
			t.Errorf("WorkspaceMasterRatio = %v, want nil", out.WorkspaceMasterRatio)
		}
		if out.MasterRatio != 0.65 || out.CurrentWorkspace != 4 {
			t.Errorf("the field an older peer does send came back as %v on workspace %d, want 0.65 on 4",
				out.MasterRatio, out.CurrentWorkspace)
		}
	})
}

// TestAPushUnionsTheMasterRatiosItDoesNotKnowAbout is the merge path. A client
// only ever holds the ratios it has been told about or tuned itself, so letting
// a push replace the set would drop every workspace that client had not heard of
// - which is the bug this field exists to stop, one layer further down.
//
// NEGATIVE CONTROL: replace the union in retainDaemonExclusive with a plain nil
// check and workspace 3 vanishes from the session.
func TestAPushUnionsTheMasterRatiosItDoesNotKnowAbout(t *testing.T) {
	canonical := &SessionState{
		Name:                 "s",
		CurrentWorkspace:     3,
		MasterRatio:          0.7,
		WorkspaceMasterRatio: map[int]float64{1: 0.5, 3: 0.7},
		Version:              1,
	}
	incoming := &SessionState{
		Name:                 "s",
		CurrentWorkspace:     1,
		MasterRatio:          0.4,
		WorkspaceMasterRatio: map[int]float64{1: 0.4},
		Version:              1,
	}

	retainDaemonExclusive(incoming, canonical)

	want := map[int]float64{1: 0.4, 3: 0.7}
	if !maps.Equal(incoming.WorkspaceMasterRatio, want) {
		t.Errorf("merged to %v, want %v: the pushing client's own value stands and the "+
			"workspace it never visited keeps the ratio another client tuned",
			incoming.WorkspaceMasterRatio, want)
	}
}

// TestAPeerThatSaysNothingKeepsTheSessionsMasterRatios: a nil map on the wire is
// an older peer, or a client with tiling off, or (gob being gob) a client with
// nothing to say. None of those is an instruction to forget.
//
// NEGATIVE CONTROL: drop the union block and the session's ratios are gone.
func TestAPeerThatSaysNothingKeepsTheSessionsMasterRatios(t *testing.T) {
	canonical := &SessionState{WorkspaceMasterRatio: map[int]float64{2: 0.8}}
	incoming := &SessionState{}

	retainDaemonExclusive(incoming, canonical)

	if got := incoming.WorkspaceMasterRatio[2]; got != 0.8 {
		t.Errorf("workspace 2 holds %v after a push that said nothing, want the session's 0.8", got)
	}
}

// TestAStaleClientCannotDropAMasterRatioItNeverSaw drives the whole daemon-side
// path: a client pushes a snapshot built before a daemon mutation, so UpdateState
// runs reconcileStale over it as well as the union.
//
// NEGATIVE CONTROL: drop the union block from retainDaemonExclusive and the
// session loses workspace 3's ratio to a stale push.
func TestAStaleClientCannotDropAMasterRatioItNeverSaw(t *testing.T) {
	sess, err := NewSession("ratio", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)

	// Client A settles the session with workspace 3 tuned to 0.7.
	sess.UpdateState(&SessionState{
		Name:                 "ratio",
		CurrentWorkspace:     3,
		MasterRatio:          0.7,
		AutoTiling:           true,
		WorkspaceMasterRatio: map[int]float64{1: 0.5, 3: 0.7},
		BaseVersion:          1,
	})
	if got := sess.GetState().WorkspaceMasterRatio[3]; got != 0.7 {
		t.Fatalf("the session holds %v for workspace 3 after A's push, want 0.7", got)
	}

	// A daemon-side mutation moves the version on, so B's next push is stale.
	if err := sess.SetDisplayName("Ratio"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}

	// Client B has never been to workspace 3 and pushes a snapshot from before
	// the rename.
	accepted := sess.UpdateState(&SessionState{
		Name:                 "ratio",
		CurrentWorkspace:     1,
		MasterRatio:          0.5,
		AutoTiling:           true,
		WorkspaceMasterRatio: map[int]float64{1: 0.5},
		BaseVersion:          1,
	})
	if accepted {
		t.Fatalf("the stale push was accepted as sent; the case is not exercising reconcileStale")
	}

	final := sess.GetState()
	if got := final.WorkspaceMasterRatio[3]; got != 0.7 {
		t.Errorf("workspace 3 holds %v after B's push, want A's 0.7: B destroyed a ratio it never touched", got)
	}
	if got := final.WorkspaceMasterRatio[1]; got != 0.5 {
		t.Errorf("workspace 1 holds %v, want the 0.5 B pushed", got)
	}
}

// TestTheStateSnapshotDoesNotShareItsMasterRatioMap: GetState hands a copy out to
// every caller, and a shared map would let one of them write into the canonical
// state without the lock.
//
// NEGATIVE CONTROL: remove the clone from snapshotStateLocked and the write
// through the snapshot reaches the session.
func TestTheStateSnapshotDoesNotShareItsMasterRatioMap(t *testing.T) {
	sess, err := NewSession("snap", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	sess.UpdateState(&SessionState{
		Name:                 "snap",
		CurrentWorkspace:     1,
		MasterRatio:          0.5,
		WorkspaceMasterRatio: map[int]float64{1: 0.5},
		BaseVersion:          1,
	})

	snap := sess.GetState()
	snap.WorkspaceMasterRatio[1] = 0.9

	if got := sess.GetState().WorkspaceMasterRatio[1]; got != 0.5 {
		t.Errorf("the session holds %v after a write through a snapshot, want the 0.5 it was given", got)
	}
}
