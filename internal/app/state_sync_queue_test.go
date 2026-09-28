package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestQueueKeepsTheNewestSnapshotTaken: the daemon writes to one client from
// more than one goroutine, so a snapshot taken earlier can be read later. The
// queue keeps the one taken last, whatever order they were read in.
//
// Negative control: with the SnapshotSeq comparison removed from
// QueueStateSync, the queue holds snapshot 5 and the check fails.
func TestQueueKeepsTheNewestSnapshotTaken(t *testing.T) {
	m := &OS{StateSyncChan: make(chan StateSyncMsg, 1)}
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 7}})
	if !m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 5}}) {
		t.Fatal("a second snapshot into a full queue did not report a displacement")
	}
	if got := (<-m.StateSyncChan).State.SnapshotSeq; got != 7 {
		t.Fatalf("the queue holds snapshot %d, want the newest taken, 7", got)
	}

	// Read in order, the later one wins as before.
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 8}})
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 9}})
	if got := (<-m.StateSyncChan).State.SnapshotSeq; got != 9 {
		t.Fatalf("the queue holds snapshot %d, want 9", got)
	}

	// Across attaches the numbers start over, so the later arrival wins.
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 900}, Attach: 1})
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 2}, Attach: 2})
	if got := (<-m.StateSyncChan).State.SnapshotSeq; got != 2 {
		t.Fatalf("a snapshot from before a new attach outranked one after it: got %d", got)
	}

	// A daemon that does not number its snapshots is read by arrival.
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{SnapshotSeq: 3}})
	m.QueueStateSync(StateSyncMsg{State: &session.SessionState{}})
	if got := (<-m.StateSyncChan).State.SnapshotSeq; got != 0 {
		t.Fatalf("an unnumbered snapshot lost to a numbered one: got %d", got)
	}
}
