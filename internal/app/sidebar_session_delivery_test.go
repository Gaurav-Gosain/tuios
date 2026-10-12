package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Saturation regression: an incoming last state may be lost behind older
// snapshots; left and right rails must not evict one another; an old client's
// late callback must not replace a new generation; and a state replacement
// must never consume the terminal-event queue. E2E cannot reliably force the
// read-loop/UI scheduling needed to fill this queue.
func TestSidebarSessionStateQueueKeepsLatestPerEdge(t *testing.T) {
	m := &OS{sidebarSessionEvents: make(chan sidebarSessionEvent, 1)}
	for _, edge := range []sidebarEdge{sidebarLeft, sidebarRight} {
		m.sidebarSessionStateEvents[edge] = make(chan sidebarSessionEvent, 1)
	}
	m.sidebarSessionEvents <- sidebarSessionEvent{kind: "ended", edge: sidebarLeft}
	for n := uint64(1); n <= 65; n++ {
		queueSidebarSessionState(m.sidebarSessionStateEvents[sidebarLeft], &m.sidebarSessionStateMu[sidebarLeft], sidebarSessionEvent{kind: "state", edge: sidebarLeft, generation: 2, state: &session.SessionState{SnapshotSeq: n}})
	}
	queueSidebarSessionState(m.sidebarSessionStateEvents[sidebarRight], &m.sidebarSessionStateMu[sidebarRight], sidebarSessionEvent{kind: "state", edge: sidebarRight, generation: 2, state: &session.SessionState{SnapshotSeq: 17}})
	queueSidebarSessionState(m.sidebarSessionStateEvents[sidebarLeft], &m.sidebarSessionStateMu[sidebarLeft], sidebarSessionEvent{kind: "state", edge: sidebarLeft, generation: 1, state: &session.SessionState{SnapshotSeq: 1000}})
	queueSidebarSessionState(m.sidebarSessionStateEvents[sidebarLeft], &m.sidebarSessionStateMu[sidebarLeft], sidebarSessionEvent{kind: "state", edge: sidebarLeft, generation: 2, state: &session.SessionState{SnapshotSeq: 64}})
	if got := (<-m.sidebarSessionStateEvents[sidebarLeft]).state.SnapshotSeq; got != 65 {
		t.Fatalf("left state seq = %d, want latest 65", got)
	}
	if got := (<-m.sidebarSessionStateEvents[sidebarRight]).state.SnapshotSeq; got != 17 {
		t.Fatalf("right state seq = %d, want 17", got)
	}
	if got := (<-m.sidebarSessionEvents).kind; got != "ended" {
		t.Fatalf("terminal event displaced by state: %q", got)
	}
}

// Repeated snapshot/subscription errors must keep the attachment failure
// count. A successful attach starts snapshots; it is not yet a usable viewer.
// Only after every pending pane subscribes can the backoff reset.
func TestSidebarSessionBackoffWaitsForSubscriptions(t *testing.T) {
	v := &sidebarSessionView{failures: 3, pending: map[string]bool{"pty": true}}
	v.resetFailuresWhenReady()
	if v.failures != 3 {
		t.Fatalf("failed to retain backoff while snapshot is pending: %d", v.failures)
	}
	delete(v.pending, "pty")
	v.resetFailuresWhenReady()
	if v.failures != 0 {
		t.Fatalf("successful subscription did not reset backoff: %d", v.failures)
	}
}
