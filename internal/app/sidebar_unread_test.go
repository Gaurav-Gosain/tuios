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

// TestATurnBetweenTwoSyncsRaisesTheDoneAlert: the folded turn is a finish
// like any other, so the alert policy hears of it.
//
// Negative control: with the considerAgentAlert call cut from
// noteAgentTurnWithin, the second done raises no dock message.
func TestATurnBetweenTwoSyncsRaisesTheDoneAlert(t *testing.T) {
	m := alertOS(t, zeroSettle())
	captureHost(t, m)
	w := m.Windows[1]
	sync := func(seq uint64) {
		m.updateWindowFromState(w, &session.WindowState{
			ID: w.ID, CustomName: w.ID, Workspace: 1,
			AgentState: session.AgentStateDone, CompletionSeq: seq,
		})
	}
	sync(0)
	m.Notifications = nil
	sync(1)
	if len(m.Notifications) != 1 {
		t.Fatalf("the folded finish raised %d dock messages, want 1", len(m.Notifications))
	}
}

// TestASyncThatFocusesAPaneJudgesItsTurnByTheNewFocus: one sync moves the
// focus to a pane and carries a turn that pane finished. The windows are
// updated before the focus is adopted, and the turn is still judged by the
// focus the sync names: the user is looking at the pane, so it is seen.
//
// Negative control: with the turnsWithinSync deferral cut from
// updateWindowFromState, the turn is judged by the old focus and reads as
// unread.
func TestASyncThatFocusesAPaneJudgesItsTurnByTheNewFocus(t *testing.T) {
	m := alertOS(t, zeroSettle())
	state := func(focus string, seq uint64) *session.SessionState {
		return &session.SessionState{
			Name: "s", CurrentWorkspace: 1, FocusedWindowID: focus,
			Windows: []session.WindowState{
				{ID: "w-1", CustomName: "w-1", Workspace: 1},
				{ID: "w-2", CustomName: "w-2", Workspace: 1, AgentState: session.AgentStateDone, CompletionSeq: seq},
			},
		}
	}
	// The first done, seen while the pane had the focus.
	if err := m.ApplyStateSync(state("w-2", 0)); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	if err := m.ApplyStateSync(state("w-1", 0)); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	// One sync: back to w-2, and w-2 finished another turn meanwhile.
	if err := m.ApplyStateSync(state("w-2", 1)); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}
	if got := m.GetFocusedWindow(); got == nil || got.ID != "w-2" {
		t.Fatalf("setup: focus = %v, want w-2", got)
	}
	w := m.windowByID("w-2")
	if state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq); state != "done" || !seen {
		t.Fatalf("got %q seen=%v, want done seen: the user is looking at it", state, seen)
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
