package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func newCycleTestOS(numWorkspaces int, startWorkspace int) *OS {
	return &OS{
		Settings:             config.Global,
		NumWorkspaces:        numWorkspaces,
		CurrentWorkspace:     startWorkspace,
		FocusedWindow:        -1,
		WorkspaceFocus:       map[int]int{},
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
		Windows:              []*terminal.Window{},
	}
}

func TestNextWorkspaceSequentialAndWrapAround(t *testing.T) {
	numWorkspaces := 9
	o := newCycleTestOS(numWorkspaces, 1)

	for expected := 2; expected <= numWorkspaces; expected++ {
		o.NextWorkspace()
		if o.CurrentWorkspace != expected {
			t.Fatalf("NextWorkspace() = %d, want %d", o.CurrentWorkspace, expected)
		}
	}

	// Boundary wrap-around from NumWorkspaces to 1
	o.NextWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("NextWorkspace() from %d = %d, want wrap-around to 1", numWorkspaces, o.CurrentWorkspace)
	}

	// Step forward again to ensure seamless cycle continuation
	o.NextWorkspace()
	if o.CurrentWorkspace != 2 {
		t.Fatalf("NextWorkspace() after wrap-around = %d, want 2", o.CurrentWorkspace)
	}
}

func TestPrevWorkspaceSequentialAndWrapAround(t *testing.T) {
	numWorkspaces := 9
	o := newCycleTestOS(numWorkspaces, 1)

	// Boundary wrap-around from 1 to NumWorkspaces
	o.PrevWorkspace()
	if o.CurrentWorkspace != numWorkspaces {
		t.Fatalf("PrevWorkspace() from 1 = %d, want wrap-around to %d", o.CurrentWorkspace, numWorkspaces)
	}

	// Step backward sequentially
	for expected := numWorkspaces - 1; expected >= 1; expected-- {
		o.PrevWorkspace()
		if o.CurrentWorkspace != expected {
			t.Fatalf("PrevWorkspace() = %d, want %d", o.CurrentWorkspace, expected)
		}
	}

	// Wrap around once more
	o.PrevWorkspace()
	if o.CurrentWorkspace != numWorkspaces {
		t.Fatalf("PrevWorkspace() from 1 = %d, want wrap-around to %d", o.CurrentWorkspace, numWorkspaces)
	}
}

func TestWorkspaceCycleCustomBounds(t *testing.T) {
	numWorkspaces := 4
	o := newCycleTestOS(numWorkspaces, 1)

	sequence := []int{2, 3, 4, 1, 2, 3, 4, 1}
	for i, want := range sequence {
		o.NextWorkspace()
		if o.CurrentWorkspace != want {
			t.Fatalf("step %d: NextWorkspace() = %d, want %d", i, o.CurrentWorkspace, want)
		}
	}

	prevSequence := []int{4, 3, 2, 1, 4, 3, 2, 1}
	for i, want := range prevSequence {
		o.PrevWorkspace()
		if o.CurrentWorkspace != want {
			t.Fatalf("step %d: PrevWorkspace() = %d, want %d", i, o.CurrentWorkspace, want)
		}
	}
}

func TestWorkspaceCycleSingleWorkspace(t *testing.T) {
	o := newCycleTestOS(1, 1)

	o.NextWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("NextWorkspace() with 1 workspace = %d, want 1", o.CurrentWorkspace)
	}

	o.PrevWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("PrevWorkspace() with 1 workspace = %d, want 1", o.CurrentWorkspace)
	}
}

func TestWorkspaceCycleOutOfBoundsRecovery(t *testing.T) {
	o := newCycleTestOS(5, 0)
	o.NextWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("NextWorkspace() recovering from 0 = %d, want 1", o.CurrentWorkspace)
	}

	o.CurrentWorkspace = 10
	o.NextWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("NextWorkspace() recovering from 10 = %d, want 1", o.CurrentWorkspace)
	}

	o.CurrentWorkspace = 0
	o.PrevWorkspace()
	if o.CurrentWorkspace != 5 {
		t.Fatalf("PrevWorkspace() recovering from 0 = %d, want 5", o.CurrentWorkspace)
	}

	o.CurrentWorkspace = 10
	o.PrevWorkspace()
	if o.CurrentWorkspace != 5 {
		t.Fatalf("PrevWorkspace() recovering from 10 = %d, want 5", o.CurrentWorkspace)
	}
}

func TestWorkspaceCycleWithPanesAndEmptyWorkspaces(t *testing.T) {
	o := newCycleTestOS(4, 1)
	// Add panes to workspace 1 and 3, leaving 2 and 4 empty.
	o.Windows = []*terminal.Window{
		{ID: "pane-1", Workspace: 1},
		{ID: "pane-3", Workspace: 3},
	}

	// Sequential next cycles through all workspaces, including empty ones.
	o.NextWorkspace()
	if o.CurrentWorkspace != 2 {
		t.Fatalf("NextWorkspace() = %d, want 2 (empty workspace should not be skipped)", o.CurrentWorkspace)
	}
	o.NextWorkspace()
	if o.CurrentWorkspace != 3 {
		t.Fatalf("NextWorkspace() = %d, want 3", o.CurrentWorkspace)
	}
	o.NextWorkspace()
	if o.CurrentWorkspace != 4 {
		t.Fatalf("NextWorkspace() = %d, want 4 (empty workspace should not be skipped)", o.CurrentWorkspace)
	}
	o.NextWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("NextWorkspace() = %d, want 1 (wrapped)", o.CurrentWorkspace)
	}

	// Sequential prev cycles backwards through all workspaces.
	o.PrevWorkspace()
	if o.CurrentWorkspace != 4 {
		t.Fatalf("PrevWorkspace() = %d, want 4 (wrapped to empty workspace)", o.CurrentWorkspace)
	}
	o.PrevWorkspace()
	if o.CurrentWorkspace != 3 {
		t.Fatalf("PrevWorkspace() = %d, want 3", o.CurrentWorkspace)
	}
	o.PrevWorkspace()
	if o.CurrentWorkspace != 2 {
		t.Fatalf("PrevWorkspace() = %d, want 2", o.CurrentWorkspace)
	}
	o.PrevWorkspace()
	if o.CurrentWorkspace != 1 {
		t.Fatalf("PrevWorkspace() = %d, want 1", o.CurrentWorkspace)
	}
}
