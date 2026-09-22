package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// railWindow returns the rail input for one window of the attached session.
func railWindow(t *testing.T, m *OS, id string) sessiontree.WindowInput {
	t.Helper()
	for _, w := range m.currentSessionInput().Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("window %s not in the rail input", id)
	return sessiontree.WindowInput{}
}

// TestRailMarksAFinishedTurnUnread checks a pane that went back to rest after
// a turn is drawn as finished and unread until this client's user looks at it,
// with no done report anywhere.
func TestRailMarksAFinishedTurnUnread(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.SidebarAgentSeenSeq = nil
	idle := m.Windows[0]
	idle.AgentCompletionSeq = 1
	m.FocusedWindow = 1

	if w := railWindow(t, m, "w-idle"); w.AgentState != "done" || w.DoneSeen {
		t.Fatalf("finished turn drawn as %q seen=%v, want done unread", w.AgentState, w.DoneSeen)
	}

	m.markFocusedAgentSeen(0)
	if w := railWindow(t, m, "w-idle"); w.AgentState != "idle" {
		t.Fatalf("after a look the pane is drawn as %q, want idle", w.AgentState)
	}

	// The next finished turn is unread again.
	idle.AgentCompletionSeq = 2
	if w := railWindow(t, m, "w-idle"); w.AgentState != "done" || w.DoneSeen {
		t.Fatalf("second turn drawn as %q seen=%v, want done unread", w.AgentState, w.DoneSeen)
	}

	// A pane back at work is drawn working whatever it finished before.
	work := m.Windows[1]
	work.AgentCompletionSeq = 5
	if w := railWindow(t, m, "w-work"); w.AgentState != "working" {
		t.Fatalf("working pane drawn as %q", w.AgentState)
	}
}

// TestRailTurnFinishedInFrontOfTheUserIsSeen checks a turn that ends in the
// focused pane is not reported to the user who watched it end.
func TestRailTurnFinishedInFrontOfTheUserIsSeen(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.SidebarAgentSeenSeq = nil
	w := m.Windows[1] // working
	m.FocusedWindow = 1
	w.AgentCompletionSeq = 1
	m.noteAgentState(w, "idle")
	if got := railWindow(t, m, "w-work"); got.AgentState != "idle" {
		t.Fatalf("a turn finished in the focused pane is drawn as %q, want idle", got.AgentState)
	}
}
