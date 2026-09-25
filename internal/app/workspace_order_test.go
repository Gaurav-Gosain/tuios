package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAnUnarrangedWorkspaceKeepsItsNumericPlace: a workspace made after the
// arrangement was set appends where the numbering put it rather than pushing
// into the middle of an order the user chose. This is the rule the rail already
// applies to a session nobody has dragged.
func TestAnUnarrangedWorkspaceKeepsItsNumericPlace(t *testing.T) {
	m := pillOS(t, 120, nil, 1, 2, 3, 4)
	m.adoptSessionLabels(&session.SessionState{WorkspaceOrder: []int{3, 1}})

	if got := m.occupiedWorkspaces(); !slices.Equal(got, []int{3, 1, 2, 4}) {
		t.Errorf("the strip lists %v, want the arranged pair then the rest by number", got)
	}
}

// TestArrangingMovesNothingThatAddressesAWorkspace is the whole reason the drag
// sets an order rather than renumbering. Every address a workspace has is by
// number, and a gesture that meant "put this one first" must not touch any of
// them.
func TestArrangingMovesNothingThatAddressesAWorkspace(t *testing.T) {
	m := pillOS(t, 120, map[int]string{1: "editor", 3: "deploy"}, 1, 2, 3)
	before := make([]int, 0, len(m.Windows))
	for _, w := range m.Windows {
		before = append(before, w.Workspace)
	}
	current := m.CurrentWorkspace

	m.adoptSessionLabels(&session.SessionState{
		WorkspaceNames: map[int]string{1: "editor", 3: "deploy"},
		WorkspaceOrder: []int{3, 2, 1},
	})

	for i, w := range m.Windows {
		if w.Workspace != before[i] {
			t.Errorf("window %d moved from workspace %d to %d", i, before[i], w.Workspace)
		}
	}
	if m.CurrentWorkspace != current {
		t.Errorf("the current workspace moved from %d to %d", current, m.CurrentWorkspace)
	}
	if m.WorkspaceLabel(1) != "editor" || m.WorkspaceLabel(3) != "deploy" {
		t.Errorf("a name followed the arrangement instead of its number: 1=%q 3=%q",
			m.WorkspaceLabel(1), m.WorkspaceLabel(3))
	}
}
