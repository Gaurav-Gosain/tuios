package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// splitOS is one tiled BSP pane on workspace 1, the state a fresh session with
// one window is in.
func splitOS(t *testing.T) *OS {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	m := &OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            200,
		Height:           60,
		EffectiveWidth:   200,
		EffectiveHeight:  60,
		AutoTiling:       true,
		UseBSPLayout:     true,
		Windows:          []*terminal.Window{{ID: "window-0001", Workspace: 1, Width: 200, Height: 60}},
		FocusedWindow:    0,
	}
	m.TileAllWindows()
	return m
}

// split is leader+- or leader+| on the focused pane, minus the PTY: it is the
// tree half of SplitFocusedHorizontal/SplitFocusedVertical.
func split(m *OS, targetID string, dir layout.PreselectionDir) *terminal.Window {
	win := &terminal.Window{
		ID:        fmt.Sprintf("window-%04d", len(m.Windows)+1),
		Workspace: m.CurrentWorkspace,
		Width:     m.Width,
		Height:    m.Height,
	}
	m.Windows = append(m.Windows, win)
	m.FocusedWindow = len(m.Windows) - 1
	m.SplitTargetWindowID = targetID
	m.PreselectionDir = dir
	m.AddWindowToBSPTree(win)
	m.SplitTargetWindowID = ""
	return win
}

// checkWorkspaceLayout asserts the panes the user can see on this workspace
// exactly tile the content region, and that the tree agrees with the window list.
func checkWorkspaceLayout(t *testing.T, m *OS, what string) {
	t.Helper()

	visible := map[int]string{}
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			visible[m.GetWindowIntID(w.ID)] = w.ID
		}
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		if len(visible) > 0 {
			t.Errorf("%s: %d visible panes but no tree to place them", what, len(visible))
		}
		return
	}

	inTree := map[int]bool{}
	for _, id := range tree.GetAllWindowIDs() {
		if _, ok := visible[id]; !ok {
			t.Errorf("%s: the tree still holds window %d, which is gone", what, id)
		}
		inTree[id] = true
	}
	for id, stringID := range visible {
		if !inTree[id] {
			t.Errorf("%s: visible pane %s (%d) has no tile", what, stringID, id)
		}
	}
	if got, want := tree.WindowCount(), len(tree.GetAllWindowIDs()); got != want {
		t.Errorf("%s: the index holds %d windows, the tree holds %d", what, got, want)
	}

	bounds := m.GetBSPBounds()
	covered := map[[2]int]int{}
	for id, r := range tree.ApplyLayout(bounds, m.separatorGap()) {
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("%s: window %d has size %dx%d", what, id, r.W, r.H)
			continue
		}
		if r.X < bounds.X || r.Y < bounds.Y || r.X+r.W > bounds.X+bounds.W || r.Y+r.H > bounds.Y+bounds.H {
			t.Errorf("%s: window %d at %+v escapes the content region %+v", what, id, r, bounds)
			continue
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if other, taken := covered[[2]int{x, y}]; taken {
					t.Fatalf("%s: windows %d and %d overlap at (%d,%d)", what, other, id, x, y)
				}
				covered[[2]int{x, y}] = id
			}
		}
	}
	if len(covered) != bounds.W*bounds.H {
		t.Errorf("%s: the panes cover %d cells of %d, leaving a gap", what, len(covered), bounds.W*bounds.H)
	}
}

// TestUnevenSplitsSurviveClosingAPane walks the reported sequence: nested
// uneven splits with leader+- and leader+|, then leader+x on each pane in turn.
func TestUnevenSplitsSurviveClosingAPane(t *testing.T) {
	down, right := layout.PreselectionDown, layout.PreselectionRight

	shapes := map[string]func(m *OS){
		"nested right": func(m *OS) {
			split(m, "window-0001", right)
			split(m, "window-0002", down)
			split(m, "window-0003", right)
		},
		"stairs": func(m *OS) {
			split(m, "window-0001", down)
			split(m, "window-0002", right)
			split(m, "window-0003", down)
		},
		"one deep side": func(m *OS) {
			split(m, "window-0001", right)
			split(m, "window-0002", down)
			split(m, "window-0003", down)
		},
		"fan from the first": func(m *OS) {
			split(m, "window-0001", right)
			split(m, "window-0001", down)
			split(m, "window-0001", right)
		},
	}

	for name, build := range shapes {
		t.Run(name, func(t *testing.T) {
			for victim := range 4 {
				m := splitOS(t)
				build(m)
				skewTreeRatios(m.WorkspaceTrees[m.CurrentWorkspace], 0.25)
				m.ApplyBSPLayout()
				checkWorkspaceLayout(t, m, fmt.Sprintf("%s (before closing)", name))

				what := fmt.Sprintf("%s closing from index %d", name, victim)
				for k := range 4 {
					idx := (victim + k) % len(m.Windows)
					m.DeleteWindow(idx)
					checkWorkspaceLayout(t, m, fmt.Sprintf("%s, %d left", what, len(m.Windows)))
				}
			}
		})
	}
}

func skewTreeRatios(tree *layout.BSPTree, ratio float64) {
	if tree == nil {
		return
	}
	i := 0
	var walk func(n *layout.TileNode)
	walk = func(n *layout.TileNode) {
		if n == nil || n.IsLeaf() {
			return
		}
		if i%2 == 0 {
			n.SplitRatio = ratio
		} else {
			n.SplitRatio = 1 - ratio
		}
		i++
		walk(n.Left)
		walk(n.Right)
	}
	walk(tree.Root)
}

// TestClosingAPaneOnAnotherWorkspaceLeavesNoStaleTile covers the pane whose
// shell exits while the user is looking at a different workspace: the ticker
// sweep closes it wherever it lives, so the tree that must lose the leaf is the
// window's own, not the one on screen.
func TestClosingAPaneOnAnotherWorkspaceLeavesNoStaleTile(t *testing.T) {
	m := splitOS(t)
	split(m, "window-0001", layout.PreselectionRight)

	// Two more panes on workspace 2, built the same way.
	m.CurrentWorkspace = 2
	m.Windows = append(m.Windows, &terminal.Window{ID: "window-0003", Workspace: 2, Width: 200, Height: 60})
	m.FocusedWindow = len(m.Windows) - 1
	m.TileAllWindows()
	split(m, "window-0003", layout.PreselectionDown)

	// Back to workspace 1; the shell in window-0004 exits on workspace 2.
	m.CurrentWorkspace = 1
	m.FocusedWindow = 0
	gone := m.GetWindowIntID("window-0004")
	for i, w := range m.Windows {
		if w.ID == "window-0004" {
			m.DeleteWindow(i)
			break
		}
	}

	if tree := m.WorkspaceTrees[2]; tree != nil && tree.HasWindow(gone) {
		t.Errorf("workspace 2 still holds a tile for the closed pane; tree holds %v", tree.GetAllWindowIDs())
	}
}
