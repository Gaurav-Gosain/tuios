package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Dragging a workspace pill sets a display order and moves no workspace. These
// pin both halves: every surface that lists workspaces reads the one order, and
// nothing that addresses a workspace by number goes near it.

// TestOneOrderArrangesEverySurface is the coherence the reorder is worth having
// for. The dock strip, the workspace switcher and the rail's terminals section
// all list the same workspaces, and a drag that rearranged one of them would
// leave the user with two arrangements to hold in their head.
func TestOneOrderArrangesEverySurface(t *testing.T) {
	m := pillOS(t, 120, map[int]string{1: "editor", 2: "review", 3: "deploy"}, 1, 2, 3)
	m.adoptSessionLabels(&session.SessionState{
		WorkspaceNames: map[int]string{1: "editor", 2: "review", 3: "deploy"},
		WorkspaceOrder: []int{3, 1, 2},
	})

	if got := m.occupiedWorkspaces(); !slices.Equal(got, []int{3, 1, 2}) {
		t.Errorf("the dock strip lists %v, want the session's order", got)
	}

	items := m.buildWorkspaceItems()
	numbers := make([]int, 0, len(items))
	for _, it := range items {
		numbers = append(numbers, it.Number)
	}
	if !slices.Equal(numbers, []int{3, 1, 2}) {
		t.Errorf("the workspace switcher lists %v, want the session's order", numbers)
	}

	// The rail groups panes by workspace off the same ranking.
	if a, b := m.workspaceRank(3), m.workspaceRank(1); a >= b {
		t.Errorf("the rail ranks workspace 3 at %d and workspace 1 at %d, want 3 first", a, b)
	}
}

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

// TestNoOrderIsTheOrderEveryWorkspaceAlreadyHad: nearly every session has never
// been rearranged, and must read exactly as it did before the order existed.
func TestNoOrderIsTheOrderEveryWorkspaceAlreadyHad(t *testing.T) {
	m := pillOS(t, 120, nil, 1, 2, 3)
	if got := m.occupiedWorkspaces(); !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("an unarranged session lists %v, want ascending", got)
	}
}

// TestADraggedOrderIsWhatTheStripDraws: the draft the pointer is building is
// live state, so the strip has to draw it rather than the committed order, or
// the pills would only move when the button came up.
func TestADraggedOrderIsWhatTheStripDraws(t *testing.T) {
	m := pillOS(t, 120, nil, 1, 2, 3)
	m.dockWorkspaceDrag = dockWorkspaceDragState{Dragging: true, Workspace: 1, Order: []int{2, 3, 1}}

	if got := m.occupiedWorkspaces(); !slices.Equal(got, []int{2, 3, 1}) {
		t.Errorf("mid-drag the strip lists %v, want the draft", got)
	}
	// A push landing mid-drag must not snap the pills back under the pointer.
	m.adoptSessionLabels(&session.SessionState{WorkspaceOrder: []int{3, 2, 1}})
	if got := m.occupiedWorkspaces(); !slices.Equal(got, []int{2, 3, 1}) {
		t.Errorf("a state push mid-drag replaced the draft with %v", got)
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
