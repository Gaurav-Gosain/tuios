package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestUnnamedSessionPresentsAsItsIdentity pins the no-op case: a session with no
// display name must render exactly the string it rendered before display names
// existed, in both the title and the id.
func TestUnnamedSessionPresentsAsItsIdentity(t *testing.T) {
	m := &OS{
		Settings:    config.Global,
		SessionName: "work",
		Windows:     []*terminal.Window{{ID: "w1"}},
	}

	got := m.BuildSessionTree().Sessions[0]
	if got.ID != "work" || got.Title != "work" {
		t.Fatalf("unnamed session = {ID:%q Title:%q}, want both %q", got.ID, got.Title, "work")
	}
}

// TestDisplayRenameLeavesTheIdentityKeysAlone is the regression this whole
// design exists to prevent. A display rename must move the label and nothing
// else: the node id, the rail's row targets and the rail's drag order are all
// keyed on the identity name and must survive the rename untouched.
func TestDisplayRenameLeavesTheIdentityKeysAlone(t *testing.T) {
	m := &OS{
		Settings:     config.Global,
		SessionName:  "work",
		Windows:      []*terminal.Window{{ID: "w1"}},
		SidebarOrder: []string{"other", "work"},
	}

	before := m.BuildSessionTree().Sessions[0]
	if before.Title != "work" {
		t.Fatalf("precondition: Title = %q, want work", before.Title)
	}

	m.SessionDisplayName = "Payments API"
	after := m.BuildSessionTree().Sessions[0]

	if after.Title != "Payments API" {
		t.Errorf("Title = %q, want the display name", after.Title)
	}
	if after.ID != "work" {
		t.Fatalf("ID = %q, want the identity name %q: renaming the identity orphans the session's persisted state", after.ID, "work")
	}

	// The rail's own row targets are the other half of the split: a click or an
	// enter has to address the session the daemon knows, whatever the row says.
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.Width, m.Height = 120, 30
	m.EffectiveWidth, m.EffectiveHeight = 120, 30
	m.sidebarPanelLinesForTree(m.BuildSessionTree())
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession {
			found = true
			if h.SessionID != "work" {
				t.Errorf("session row targets %q, want the identity work", h.SessionID)
			}
		}
	}
	if !found {
		t.Error("no session row was drawn")
	}

	// The rail's drag order is keyed on the identity too, and it is persisted,
	// so a miss here corrupts sidebar.json rather than just the frame.
	inputs := []sessiontree.SessionInput{
		{Name: "work", DisplayName: "Payments API"},
		{Name: "other"},
	}
	ordered := orderByKey(inputs, func(s sessiontree.SessionInput) string { return s.Name }, m.SidebarOrder)
	if ordered[0].Name != "other" || ordered[1].Name != "work" {
		t.Errorf("order after display rename = %q, %q; want other, work", ordered[0].Name, ordered[1].Name)
	}
}

// TestRenameSurvivesAReattach simulates the reattach: the client throws its
// state away and restores from the daemon's snapshot. The label is daemon-owned,
// so it must come back with it rather than reverting to the identity name.
func TestRenameSurvivesAReattach(t *testing.T) {
	m := &OS{Settings: config.Global, SessionName: "work", Windows: []*terminal.Window{{ID: "w1"}}}

	if err := m.RestoreFromState(&session.SessionState{
		Name:             "work",
		CurrentWorkspace: 1,
		DisplayName:      "Payments API",
		WorkspaceNames:   map[int]string{2: "review"},
	}); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}

	if got := m.SessionLabel("work"); got != "Payments API" {
		t.Errorf("session label after reattach = %q, want Payments API", got)
	}
	if got := m.WorkspaceLabel(2); got != "review" {
		t.Errorf("workspace label after reattach = %q, want review", got)
	}
	if m.SessionName != "work" {
		t.Errorf("SessionName = %q, want the identity work", m.SessionName)
	}
	if got := m.BuildSessionTree().Sessions[0]; got.ID != "work" || got.Title != "Payments API" {
		t.Errorf("tree row after reattach = {ID:%q Title:%q}, want {work Payments API}", got.ID, got.Title)
	}
}

// TestWorkspaceLabelFallsBackToTheNumber checks an unnamed workspace still
// presents as its number, which is both its identity and the label it has
// always shown.
func TestWorkspaceLabelFallsBackToTheNumber(t *testing.T) {
	m := &OS{Settings: config.Global}
	if got := m.WorkspaceLabel(3); got != "3" {
		t.Errorf("unnamed workspace label = %q, want %q", got, "3")
	}

	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{2: "review"}})
	if got := m.WorkspaceLabel(2); got != "review" {
		t.Errorf("named workspace label = %q, want review", got)
	}
	if got := m.WorkspaceLabel(3); got != "3" {
		t.Errorf("sibling of a named workspace = %q, want %q", got, "3")
	}
}

// TestAdoptSessionLabelsCopiesTheMap guards against the model aliasing a state
// push's map, which would let a later daemon mutation change the client's
// labels behind its back.
func TestAdoptSessionLabelsCopiesTheMap(t *testing.T) {
	names := map[int]string{1: "review"}
	m := &OS{Settings: config.Global}
	m.adoptSessionLabels(&session.SessionState{DisplayName: "Payments API", WorkspaceNames: names})

	names[1] = "mutated"
	if got := m.WorkspaceLabel(1); got != "review" {
		t.Errorf("workspace label = %q, want review: the model aliased the pushed map", got)
	}
	if m.SessionDisplayName != "Payments API" {
		t.Errorf("SessionDisplayName = %q, want Payments API", m.SessionDisplayName)
	}
}
