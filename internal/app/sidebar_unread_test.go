package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestATurnBetweenTwoSyncsReadsAsUnread: a done pane the user has looked at
// goes working and done again, and the daemon's next snapshot carries only the
// second done and a higher CompletionSeq. The pane reads as an unread done
// again. The seen bit from the first done used to survive, so the agents
// header never counted the second finish.
//
// Negative control: with the noteAgentTurnWithin call cut from
// updateWindowFromState, the pane stays seen and the second check fails.
func TestATurnBetweenTwoSyncsReadsAsUnread(t *testing.T) {
	m := alertOS(t, zeroSettle())
	w := m.Windows[1]
	sync := func(state session.AgentState, seq uint64) {
		m.updateWindowFromState(w, &session.WindowState{
			ID: w.ID, CustomName: w.ID, Workspace: 1,
			AgentState: state, CompletionSeq: seq,
		})
	}

	// The first done, finished under the user's eyes, is seen.
	m.FocusedWindow = 1
	sync(session.AgentStateDone, 0)
	m.FocusedWindow = 0
	if state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq); state != "done" || !seen {
		t.Fatalf("first done: got %q seen=%v, want done seen", state, seen)
	}

	// working and done again, folded into one snapshot.
	sync(session.AgentStateDone, 1)
	if state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq); state != "done" || seen {
		t.Fatalf("second done: got %q seen=%v, want an unread done", state, seen)
	}
	if c := sidebarAgentCounts([]sidebarAgentEntry{{State: "done"}}); c.Done != 1 {
		t.Fatalf("sanity: an unread done counts as done, got %+v", c)
	}
}

// TestATurnBetweenTwoSyncsInFrontOfTheUserStaysSeen: the same folded turn on
// the focused pane is one the user watched finish, so it stays seen.
func TestATurnBetweenTwoSyncsInFrontOfTheUserStaysSeen(t *testing.T) {
	m := alertOS(t, zeroSettle())
	w := m.Windows[1]
	m.FocusedWindow = 1
	for _, seq := range []uint64{0, 1} {
		m.updateWindowFromState(w, &session.WindowState{
			ID: w.ID, CustomName: w.ID, Workspace: 1,
			AgentState: session.AgentStateDone, CompletionSeq: seq,
		})
	}
	if state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq); state != "done" || !seen {
		t.Fatalf("got %q seen=%v, want done seen", state, seen)
	}
	if got := m.SidebarAgentSeenSeq[w.ID]; got != 1 {
		t.Fatalf("seen seq = %d, want 1", got)
	}
}
