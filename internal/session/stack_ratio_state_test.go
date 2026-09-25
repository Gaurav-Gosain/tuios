package session

import (
	"maps"
	"testing"
)

// TestAPushUnionsTheStackRatiosItDoesNotKnowAbout is the merge path, on the
// terms the master ratios are merged on: the pushing client's entries win, and
// a workspace it never heard of keeps the session's entry.
//
// NEGATIVE CONTROL: drop the stack ratio block from retainDaemonExclusive and
// workspace 3 and the nil push both lose the session's value.
func TestAPushUnionsTheStackRatiosItDoesNotKnowAbout(t *testing.T) {
	canonical := &SessionState{WorkspaceStackRatio: map[int]float64{1: 0.5, 3: 0.2}}
	incoming := &SessionState{WorkspaceStackRatio: map[int]float64{1: 0.4}}
	retainDaemonExclusive(incoming, canonical)
	if want := (map[int]float64{1: 0.4, 3: 0.2}); !maps.Equal(incoming.WorkspaceStackRatio, want) {
		t.Errorf("merged to %v, want %v", incoming.WorkspaceStackRatio, want)
	}

	silent := &SessionState{}
	retainDaemonExclusive(silent, canonical)
	if got := silent.WorkspaceStackRatio[3]; got != 0.2 {
		t.Errorf("workspace 3 holds %v after a push that said nothing, want the session's 0.2", got)
	}
}

// TestTheStateSnapshotDoesNotShareItsStackRatioMap: GetState hands out a copy,
// so a write through it must not reach the session.
//
// NEGATIVE CONTROL: remove the clone from snapshotStateLocked and the write
// reaches the session.
func TestTheStateSnapshotDoesNotShareItsStackRatioMap(t *testing.T) {
	sess, err := NewSession("stack", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
	sess.UpdateState(&SessionState{
		Name:                "stack",
		CurrentWorkspace:    1,
		MasterRatio:         0.5,
		WorkspaceStackRatio: map[int]float64{1: 0.3},
		BaseVersion:         1,
	})

	snap := sess.GetState()
	if got := snap.WorkspaceStackRatio[1]; got != 0.3 {
		t.Fatalf("the session holds %v for workspace 1, want the 0.3 it was given", got)
	}
	snap.WorkspaceStackRatio[1] = 0.9

	if got := sess.GetState().WorkspaceStackRatio[1]; got != 0.3 {
		t.Errorf("the session holds %v after a write through a snapshot, want 0.3", got)
	}
}
