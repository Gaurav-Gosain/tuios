package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Resizing a divider in a BSP layout may only move the two subtrees that
// divider separates. Everything else on screen has to stay exactly where it
// was, and it has to stay there through a retile as well, because a retile
// rebuilds every pane from the tree's ratios and is triggered by ordinary
// things: opening a window, closing one, switching workspace, a terminal
// resize.
//
// Two separate defects broke that. Resizes were matched to neighbours by
// geometry, collecting every pane whose edge fell on the dragged line, so a
// divider in one column dragged the identically-placed divider in another
// column with it - and fresh splits are all 0.5, so dividers line up by
// default. And the keyboard path never wrote its result back into the tree, so
// the next retile discarded the resize and snapped every pane back.

// isoOS builds an auto-tiling model with n real windows and no layout yet.
func isoOS(tb testing.TB, n int) *app.OS {
	tb.Helper()

	// Snap animations apply their target over the following frames, so with them
	// on a retile leaves geometry untouched at the instant it returns and the
	// snap-back this test is looking for would land after the assertions rather
	// than before them.
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	tb.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	m := &app.OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            160,
		Height:           48,
		AutoTiling:       true,
		UseBSPLayout:     true,
		FocusedWindow:    0,
		PendingResizes:   make(map[string][2]int),

		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceLayouts:     make(map[int][]app.WindowLayout),
		WorkspaceMasterRatio: make(map[int]float64),
	}

	for i := range n {
		id := fmt.Sprintf("iso-%d-%d", n, i+1)
		ptyData := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ptyData:
				case <-done:
					return
				}
			}
		}()
		tb.Cleanup(func() { close(done) })

		win := terminal.NewDaemonWindow(id, "test", 0, 0, 40, 12, 0, "pty-"+id, ptyData)
		if win == nil {
			tb.Fatal("NewDaemonWindow returned nil")
		}
		tb.Cleanup(win.Close)
		win.Workspace = 1
		win.Tiled = config.SharedBorders
		m.Windows = append(m.Windows, win)
	}

	return m
}

// buildTree installs a hand-built topology, mapping the small integer IDs the
// topology uses onto the model's windows in order, then lays it out.
func buildTree(m *app.OS, build func(leaf func(int) *layout.TileNode) *layout.TileNode) {
	tree := layout.NewBSPTree()
	leaf := func(i int) *layout.TileNode {
		intID := m.GetWindowIntID(m.Windows[i-1].ID)
		n := layout.NewLeafNode(intID)
		tree.WindowToNode[intID] = n
		return n
	}
	tree.Root = build(leaf)
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}
	m.WorkspaceTrees[1] = tree

	for intID, r := range tree.ApplyLayout(m.GetBSPBounds()) {
		if win := m.GetWindowByIntID(intID); win != nil {
			win.X, win.Y = r.X, r.Y
			win.Resize(r.W, r.H)
		}
	}
}

func v(ratio float64, l, r *layout.TileNode) *layout.TileNode {
	return layout.NewInternalNode(layout.SplitVertical, ratio, l, r)
}

func h(ratio float64, l, r *layout.TileNode) *layout.TileNode {
	return layout.NewInternalNode(layout.SplitHorizontal, ratio, l, r)
}

type isoCase struct {
	name  string
	count int
	build func(leaf func(int) *layout.TileNode) *layout.TileNode
	// pane and edge name the divider to drag, in 1-based topology IDs.
	pane int
	edge layout.ResizeEdge
	// affected lists every pane the divider legitimately owns. Panes outside
	// this set must not move by a single cell.
	affected []int
}

var isoCases = []isoCase{
	{
		// A 2x2 grid: the two columns each have their own horizontal divider,
		// and at the default 0.5 ratios the two sit on the same screen row.
		// Dragging the left one must not touch the right column.
		name:  "grid/left_column_divider",
		count: 4,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.5, h(0.5, leaf(1), leaf(2)), h(0.5, leaf(3), leaf(4)))
		},
		pane: 1, edge: layout.ResizeEdgeBottom,
		affected: []int{1, 2},
	},
	{
		name:  "grid/right_column_divider",
		count: 4,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.5, h(0.5, leaf(1), leaf(2)), h(0.5, leaf(3), leaf(4)))
		},
		pane: 4, edge: layout.ResizeEdgeTop,
		affected: []int{3, 4},
	},
	{
		// The root divider does separate both columns, so every pane moving is
		// correct here. This case guards the other direction: the fix must not
		// have made resizes stop propagating.
		name:  "grid/root_divider",
		count: 4,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.5, h(0.5, leaf(1), leaf(2)), h(0.5, leaf(3), leaf(4)))
		},
		pane: 1, edge: layout.ResizeEdgeRight,
		affected: []int{1, 2, 3, 4},
	},
	{
		// The maintainer's layout: one tall pane on the left, two stacked on
		// the right.
		name:  "tall_left/right_column_divider",
		count: 3,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.5, leaf(1), h(0.5, leaf(2), leaf(3)))
		},
		pane: 2, edge: layout.ResizeEdgeBottom,
		affected: []int{2, 3},
	},
	{
		// Three columns, each split in two, all dividers at the same rows.
		name:  "three_columns/middle_divider",
		count: 6,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.34, h(0.5, leaf(1), leaf(2)),
				v(0.5, h(0.5, leaf(3), leaf(4)), h(0.5, leaf(5), leaf(6))))
		},
		pane: 3, edge: layout.ResizeEdgeBottom,
		affected: []int{3, 4},
	},
	{
		name:  "three_columns/inner_column_divider",
		count: 6,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.34, h(0.5, leaf(1), leaf(2)),
				v(0.5, h(0.5, leaf(3), leaf(4)), h(0.5, leaf(5), leaf(6))))
		},
		pane: 5, edge: layout.ResizeEdgeLeft,
		affected: []int{3, 4, 5, 6},
	},
	{
		// A pane three levels down, so the divider's rectangle is narrowed by
		// several ancestors before the ratio is derived against it.
		name:  "deep_nest/innermost_divider",
		count: 5,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.4, leaf(1), h(0.6, h(0.5, leaf(2), v(0.5, leaf(3), leaf(4))), leaf(5)))
		},
		pane: 3, edge: layout.ResizeEdgeRight,
		affected: []int{3, 4},
	},
}

type geom map[string][4]int

func geomOf(m *app.OS) geom {
	g := make(geom, len(m.Windows))
	for _, w := range m.Windows {
		g[w.ID] = [4]int{w.X, w.Y, w.Width, w.Height}
	}
	return g
}

// checkIsolation is the invariant: nothing outside the divider's own two
// subtrees moved, and at least one pane inside them did.
func checkIsolation(t *testing.T, label string, c isoCase, m *app.OS, before, after geom) {
	t.Helper()

	inSubtree := make(map[string]bool, len(c.affected))
	for _, p := range c.affected {
		inSubtree[m.Windows[p-1].ID] = true
	}

	changed := false
	for id, b := range before {
		a := after[id]
		if inSubtree[id] {
			if a != b {
				changed = true
			}
			continue
		}
		if a != b {
			t.Errorf("%s: pane %s is outside the dragged divider's subtrees but moved: %v -> %v",
				label, id, b, a)
		}
	}
	if !changed {
		t.Errorf("%s: the resize did not move any pane; the drag was a no-op", label)
	}
}

// checkSurvivesRetile is the second half: a retile rebuilds every pane from the
// tree's ratios, so anything the resize failed to write into the tree is thrown
// away here.
func checkSurvivesRetile(t *testing.T, label string, m *app.OS, after geom) {
	t.Helper()

	frameBefore := m.View().Content
	m.TileAllWindows()
	for id, want := range after {
		var got [4]int
		for _, w := range m.Windows {
			if w.ID == id {
				got = [4]int{w.X, w.Y, w.Width, w.Height}
			}
		}
		if got != want {
			t.Errorf("%s: retile moved pane %s: %v -> %v; the resize was not committed to the tree",
				label, id, want, got)
		}
	}
	if frameAfter := m.View().Content; frameAfter != frameBefore {
		t.Errorf("%s: the composed frame changed across a retile that should have been a no-op", label)
	}
}

// TestKeyboardResizeMovesOnlyTheDividersSubtrees drives the keyboard path.
func TestKeyboardResizeMovesOnlyTheDividersSubtrees(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		for _, c := range isoCases {
			label := fmt.Sprintf("shared=%v/%s/keyboard", shared, c.name)
			t.Run(label, func(t *testing.T) {
				prev := config.SharedBorders
				config.SharedBorders = shared
				t.Cleanup(func() { config.SharedBorders = prev })

				m := isoOS(t, c.count)
				buildTree(m, c.build)
				m.FocusedWindow = c.pane - 1

				before := geomOf(m)
				const delta = 4
				switch c.edge {
				case layout.ResizeEdgeRight:
					m.ResizeFocusedWindowWidth(delta)
				case layout.ResizeEdgeLeft:
					m.ResizeFocusedWindowWidthLeft(delta)
				case layout.ResizeEdgeBottom:
					m.ResizeFocusedWindowHeight(-delta)
				case layout.ResizeEdgeTop:
					m.ResizeFocusedWindowHeightTop(delta)
				}
				after := geomOf(m)

				checkIsolation(t, label, c, m, before, after)
				checkSurvivesRetile(t, label, m, after)
			})
		}
	}
}

// TestMouseResizeMovesOnlyTheDividersSubtrees drives the same layouts through
// the real mouse path: press state, a run of motion events, then release.
func TestMouseResizeMovesOnlyTheDividersSubtrees(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		for _, c := range isoCases {
			label := fmt.Sprintf("shared=%v/%s/mouse", shared, c.name)
			t.Run(label, func(t *testing.T) {
				prev := config.SharedBorders
				config.SharedBorders = shared
				t.Cleanup(func() { config.SharedBorders = prev })

				m := isoOS(t, c.count)
				buildTree(m, c.build)
				m.FocusedWindow = c.pane - 1

				win := m.Windows[c.pane-1]
				// Grab the corner that owns the edge under test, then move the
				// pointer along that one axis only, so exactly one divider is
				// dragged.
				var startX, startY int
				switch c.edge {
				case layout.ResizeEdgeRight:
					m.ResizeCorner, startX, startY = app.BottomRight, win.X+win.Width, win.Y+win.Height
				case layout.ResizeEdgeBottom:
					m.ResizeCorner, startX, startY = app.BottomRight, win.X+win.Width, win.Y+win.Height
				case layout.ResizeEdgeLeft:
					m.ResizeCorner, startX, startY = app.TopLeft, win.X, win.Y
				case layout.ResizeEdgeTop:
					m.ResizeCorner, startX, startY = app.TopLeft, win.X, win.Y
				}
				m.Resizing = true
				m.InteractionMode = true
				m.PreResizeState = terminal.Window{
					ID: win.ID, X: win.X, Y: win.Y, Z: win.Z,
					Width: win.Width, Height: win.Height,
				}
				m.ResizeStartX, m.ResizeStartY = startX, startY

				before := geomOf(m)

				const steps = 4
				for i := 1; i <= steps; i++ {
					x, y := startX, startY
					switch c.edge {
					case layout.ResizeEdgeRight, layout.ResizeEdgeLeft:
						x = startX - i
					case layout.ResizeEdgeBottom, layout.ResizeEdgeTop:
						y = startY - i
					}
					_, _ = m.Update(motionAt(x, y))
					_ = m.View()
				}
				switch c.edge {
				case layout.ResizeEdgeRight, layout.ResizeEdgeLeft:
					_, _ = m.Update(releaseAt(startX-steps, startY))
				default:
					_, _ = m.Update(releaseAt(startX, startY-steps))
				}
				after := geomOf(m)

				checkIsolation(t, label, c, m, before, after)
				checkSurvivesRetile(t, label, m, after)
			})
		}
	}
}
