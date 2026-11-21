package input

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

func isoOS(tb testing.TB, n int) *app.OS {
	tb.Helper()

	prevAnim := config.Global.Motion
	config.Global.Motion = config.MotionNone
	tb.Cleanup(func() { config.Global.Motion = prevAnim })

	m := &app.OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		SharedBorders:    config.Global.SharedBorders,
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

		win := terminal.NewDaemonWindow(id, "test", 0, 0, 40, 12, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			tb.Fatal("NewDaemonWindow returned nil")
		}
		tb.Cleanup(win.Close)
		win.Workspace = 1
		win.Tiled = m.Settings.SharedBorders
		m.Windows = append(m.Windows, win)
	}

	return m
}

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

	for intID, r := range tree.ApplyLayout(m.GetBSPBounds(), sharedGap()) {
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
	name     string
	count    int
	build    func(leaf func(int) *layout.TileNode) *layout.TileNode
	pane     int
	edge     layout.ResizeEdge
	affected []int
}

var isoCases = []isoCase{
	{
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
		name:  "grid/root_divider",
		count: 4,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.5, h(0.5, leaf(1), leaf(2)), h(0.5, leaf(3), leaf(4)))
		},
		pane: 1, edge: layout.ResizeEdgeRight,
		affected: []int{1, 2, 3, 4},
	},
	{
		name:  "tall_left/right_column_divider",
		count: 3,
		build: func(leaf func(int) *layout.TileNode) *layout.TileNode {
			return v(0.5, leaf(1), h(0.5, leaf(2), leaf(3)))
		},
		pane: 2, edge: layout.ResizeEdgeBottom,
		affected: []int{2, 3},
	},
	{
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
				prev := config.Global.SharedBorders
				config.Global.SharedBorders = shared
				t.Cleanup(func() { config.Global.SharedBorders = prev })

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
				prev := config.Global.SharedBorders
				config.Global.SharedBorders = shared
				t.Cleanup(func() { config.Global.SharedBorders = prev })

				m := isoOS(t, c.count)
				buildTree(m, c.build)
				m.FocusedWindow = c.pane - 1

				win := m.Windows[c.pane-1]
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
				m.DraggedWindowIndex = c.pane - 1
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

// TestResizeSurvivesAnInFlightSnapAnimation pins the other way a pane can move
// on its own. A snap animation writes an interpolated rectangle to its window
// on every tick and does not care what else has set the geometry since, so a
// drag that begins while windows are still animating into place gets stamped
// over on the next tick and the layout jumps back to a frame of the transition
// the user had already moved past.
//
// Windows animate on any layout change (opening a pane, closing one,
// switching workspace, applying a template), so starting a drag on top of one
// is ordinary, not a corner case.
func TestResizeSurvivesAnInFlightSnapAnimation(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			prev := config.Global.SharedBorders
			config.Global.SharedBorders = shared
			t.Cleanup(func() { config.Global.SharedBorders = prev })

			m := isoOS(t, 4)
			m.Settings.Motion = config.MotionFull
			buildTree(m, func(leaf func(int) *layout.TileNode) *layout.TileNode {
				return v(0.5, h(0.5, leaf(1), leaf(2)), h(0.5, leaf(3), leaf(4)))
			})

			for _, w := range m.Windows {
				w.X, w.Y = w.X+3, w.Y+2
			}

			m.TileAllWindows()
			if !m.HasActiveAnimations() {
				t.Fatal("no snap animations queued; this test needs some in flight to be meaningful")
			}

			win := m.Windows[0]
			startX, startY := win.X+win.Width, win.Y+win.Height
			m.DraggedWindowIndex = 0
			m.Resizing = true
			m.InteractionMode = true
			m.ResizeCorner = app.BottomRight
			m.PreResizeState = terminal.Window{
				ID: win.ID, X: win.X, Y: win.Y, Z: win.Z,
				Width: win.Width, Height: win.Height,
			}
			m.ResizeStartX, m.ResizeStartY = startX, startY

			preDrag := geomOf(m)
			_, _ = m.Update(motionAt(startX-6, startY))
			if geomOf(m)[win.ID] == preDrag[win.ID] {
				t.Fatal("the motion event did not resize anything, so this test would " +
					"pass whether or not animations are retired")
			}

			for _, w := range m.Windows {
				for _, a := range m.Animations {
					if a.Window == w && a.Type == ui.AnimationSnap {
						t.Fatalf("pane %s still has a snap animation after a resize motion; "+
							"it will overwrite the drag on the next tick", w.ID)
					}
				}
			}

			settled := geomOf(m)
			for range 5 {
				_, _ = m.Update(app.TickerMsg(time.Now()))
			}
			for id, want := range settled {
				var got [4]int
				for _, w := range m.Windows {
					if w.ID == id {
						got = [4]int{w.X, w.Y, w.Width, w.Height}
					}
				}
				if got != want {
					t.Errorf("pane %s moved after the gesture ended: %v -> %v; "+
						"an in-flight snap animation overwrote the resize", id, want, got)
				}
			}
		})
	}
}
