package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Issue #234: a pane in the multifocus set takes the keys the user types, so
// dim_unfocused leaves it at full strength. A pane outside the set is dimmed
// as before. appearance.dim_multifocus turns the old look back on.
func TestMultifocusPaneIsNotDimmed(t *testing.T) {
	const undimmed = "200;200;200"
	cases := []struct {
		name          string
		dimMultifocus bool
		inSet         bool
		wantDimmed    bool
	}{
		{"in the set, default", false, true, false},
		{"outside the set, default", false, false, true},
		{"in the set, dim_multifocus on", true, true, true},
		{"outside the set, dim_multifocus on", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withDim(t, 50)
			win := dimTestWindow(t, 40, 6)
			m := newTestOS(win)
			m.Settings.DimMultifocus = tc.dimMultifocus
			if tc.inSet {
				m.MultifocusSet = map[string]bool{win.ID: true}
			}

			out := m.renderTerminal(win, false, false)
			if dimmed := !strings.Contains(out, undimmed); dimmed != tc.wantDimmed {
				t.Errorf("dimmed = %v, want %v", dimmed, tc.wantDimmed)
			}
		})
	}
}

// Multi copy mode parks a pane its search found nothing in, and draws it
// dimmed. That is a separate signal, and a parked pane in the multifocus set
// stays dimmed.
func TestMultifocusParkedPaneStaysDimmed(t *testing.T) {
	withDim(t, 0)
	win := dimTestWindow(t, 40, 6)
	m := newTestOS(win)
	m.MultifocusSet = map[string]bool{win.ID: true}
	m.MultiCopy = &MultiCopy{Parked: map[string]bool{win.ID: true}}

	out := m.renderTerminal(win, false, false)
	if strings.Contains(out, "200;200;200") {
		t.Error("a parked pane in the multifocus set was drawn undimmed")
	}
}

// Toggling a pane into the set drops its dimmed frame from the cache, so the
// change shows at once.
func TestMultifocusToggleRedrawsUndimmed(t *testing.T) {
	withDim(t, 50)
	win := dimTestWindow(t, 40, 6)
	m := newTestOS(win)

	before := m.renderTerminal(win, false, false)
	if strings.Contains(before, "200;200;200") {
		t.Fatal("the pane was not dimmed before the toggle, so this proves nothing")
	}
	m.ToggleMultifocus(0)
	after := m.renderTerminal(win, false, false)
	if !strings.Contains(after, "200;200;200") {
		t.Error("the pane is still dimmed after it joined the multifocus set")
	}
}

// Issue #232: toggle all puts the visible panes of the workspace in the set
// and clears the set when they are all in it.
func TestToggleMultifocusAll(t *testing.T) {
	a := newTestWindow(t, "a", 10, 5)
	b := newTestWindow(t, "b", 10, 5)
	hidden := newTestWindow(t, "hidden", 10, 5)
	other := newTestWindow(t, "other", 10, 5)
	m := newTestOS(a)
	m.Windows = []*terminal.Window{a, b, hidden, other}
	ws := m.CurrentWorkspace
	a.Workspace, b.Workspace, hidden.Workspace = ws, ws, ws
	hidden.Minimized = true
	other.Workspace = ws + 1

	m.ToggleMultifocusAll()
	if len(m.MultifocusSet) != 2 || !m.MultifocusSet[a.ID] || !m.MultifocusSet[b.ID] {
		t.Fatalf("set = %v, want a and b", m.MultifocusSet)
	}
	m.ToggleMultifocusAll()
	if m.MultifocusSet != nil {
		t.Fatalf("set = %v, want it cleared", m.MultifocusSet)
	}
}
