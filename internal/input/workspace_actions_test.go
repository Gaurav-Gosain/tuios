package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func TestWorkspaceActionsRegistration(t *testing.T) {
	d := GetDispatcher()
	if !d.HasAction("next_workspace") {
		t.Fatal("dispatcher does not have next_workspace registered")
	}
	if !d.HasAction("prev_workspace") {
		t.Fatal("dispatcher does not have prev_workspace registered")
	}
}

func TestWorkspaceActionsDispatchCyclingAndWrapAround(t *testing.T) {
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.CurrentWorkspace = 1
	o.NumWorkspaces = 9
	o.Windows = []*terminal.Window{
		{ID: "win-1", Workspace: 1},
	}

	d := GetDispatcher()

	// Forward sequential cycling
	for expected := 2; expected <= 9; expected++ {
		m, _ := d.Dispatch("next_workspace", tea.KeyPressMsg{}, o)
		if m.CurrentWorkspace != expected {
			t.Fatalf("Dispatch next_workspace got workspace %d, want %d", m.CurrentWorkspace, expected)
		}
	}

	// Boundary wrap-around 9 -> 1
	m, _ := d.Dispatch("next_workspace", tea.KeyPressMsg{}, o)
	if m.CurrentWorkspace != 1 {
		t.Fatalf("Dispatch next_workspace wrap-around got %d, want 1", m.CurrentWorkspace)
	}

	// Boundary wrap-around backward 1 -> 9
	m, _ = d.Dispatch("prev_workspace", tea.KeyPressMsg{}, o)
	if m.CurrentWorkspace != 9 {
		t.Fatalf("Dispatch prev_workspace wrap-around got %d, want 9", m.CurrentWorkspace)
	}

	// Backward sequential cycling 9 -> 8 -> ... -> 1
	for expected := 8; expected >= 1; expected-- {
		m, _ = d.Dispatch("prev_workspace", tea.KeyPressMsg{}, o)
		if m.CurrentWorkspace != expected {
			t.Fatalf("Dispatch prev_workspace got workspace %d, want %d", m.CurrentWorkspace, expected)
		}
	}
}

func TestWorkspaceKeybindingsCycleWorkspaces(t *testing.T) {
	o := osWithBindings(t, func(kb *config.KeybindingsConfig) {
		kb.Workspaces["next_workspace"] = []string{"]"}
		kb.Workspaces["prev_workspace"] = []string{"["}
	})
	o.Mode = 0 // WindowManagementMode
	o.CurrentWorkspace = 1
	o.NumWorkspaces = 4

	// Press "]" to cycle forward
	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: ']', Text: "]"}, o)
	if o.CurrentWorkspace != 2 {
		t.Fatalf("after ']' keypress, CurrentWorkspace = %d, want 2", o.CurrentWorkspace)
	}

	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: ']', Text: "]"}, o)
	if o.CurrentWorkspace != 3 {
		t.Fatalf("after second ']' keypress, CurrentWorkspace = %d, want 3", o.CurrentWorkspace)
	}

	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: ']', Text: "]"}, o)
	if o.CurrentWorkspace != 4 {
		t.Fatalf("after third ']' keypress, CurrentWorkspace = %d, want 4", o.CurrentWorkspace)
	}

	// Boundary wrap-around forward
	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: ']', Text: "]"}, o)
	if o.CurrentWorkspace != 1 {
		t.Fatalf("after fourth ']' keypress, CurrentWorkspace = %d, want wrap-around to 1", o.CurrentWorkspace)
	}

	// Press "[" to cycle backward with wrap-around
	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: '[', Text: "["}, o)
	if o.CurrentWorkspace != 4 {
		t.Fatalf("after '[' keypress, CurrentWorkspace = %d, want wrap-around to 4", o.CurrentWorkspace)
	}

	HandleWindowManagementModeKey(tea.KeyPressMsg{Code: '[', Text: "["}, o)
	if o.CurrentWorkspace != 3 {
		t.Fatalf("after second '[' keypress, CurrentWorkspace = %d, want 3", o.CurrentWorkspace)
	}
}
