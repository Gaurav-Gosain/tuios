package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// switcherOS builds a session switcher over a fixed item list, with a zero-value
// daemon client because the switcher's daemon-mode guard only checks for one.
func switcherOS(items []sessiontree.Node) *OS {
	m := &OS{Settings: config.Global, IsDaemonSession: true, DaemonClient: &session.TUIClient{}}
	m.ShowSessionSwitcher = true
	m.SessionSwitcherItems = items
	return m
}

func switcherItems() []sessiontree.Node {
	return []sessiontree.Node{
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name: "work", DisplayName: "Payments API", IsCurrent: true,
			Windows: []sessiontree.WindowInput{
				{ID: "w1", Title: "vim"},
				{ID: "w2", Title: "claude", AgentState: "needs_input"},
				{ID: "w3", Title: "logs", AgentState: "working"},
			},
		}),
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name:    "notes",
			Windows: []sessiontree.WindowInput{{ID: "n1", Title: "shell"}},
		}),
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name: "infra", Windows: []sessiontree.WindowInput{
				{ID: "i1", Title: "tf", AgentState: "errored"},
				{ID: "i2", Title: "sh"},
			},
		}),
	}
}

// TestSessionSwitcherFilterNarrowsAndTargetsTheFilteredRow is the classic
// overlay off-by-one: with a query typed, selection 0 must mean the first row
// on screen, not the first item in the unfiltered list.
func TestSessionSwitcherFilterNarrowsAndTargetsTheFilteredRow(t *testing.T) {
	m := switcherOS(switcherItems())

	if got := len(FilterSessionItems(m.SessionSwitcherItems, "")); got != 3 {
		t.Fatalf("unfiltered count = %d, want 3", got)
	}

	m.SessionSwitcherQuery = "inf"
	m.SessionSwitcherSelected = 0

	filtered := FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery)
	if len(filtered) != 1 {
		t.Fatalf("filtered count = %d, want 1", len(filtered))
	}
	target, ok := m.SessionSwitcherTarget(0)
	if !ok {
		t.Fatal("selection 0 resolved to nothing while a row was on screen")
	}
	if target.ID != "infra" {
		t.Errorf("target = %q, want infra: the selection was resolved against the unfiltered list", target.ID)
	}

	// A rename must not cost the user the name they know the session by.
	m.SessionSwitcherQuery = "work"
	byIdentity, ok := m.SessionSwitcherTarget(0)
	if !ok || byIdentity.ID != "work" {
		t.Errorf("searching the identity of a renamed session found %+v, want work", byIdentity)
	}

	m.SessionSwitcherQuery = "payments"
	byLabel, ok := m.SessionSwitcherTarget(0)
	if !ok || byLabel.ID != "work" {
		t.Errorf("searching the display name found %+v, want the work session", byLabel)
	}

	m.SessionSwitcherQuery = "nothing-matches-this"
	if _, ok := m.SessionSwitcherTarget(0); ok {
		t.Error("an empty filtered list still resolved a target")
	}
}
