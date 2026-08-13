package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A pane under shared borders has no border of its own, so the only thing that
// says where one ends is the divider grid. These tests read the grid as motion
// rather than as a still: they step a transition through its duration and assert
// the invariant the grid holds on every frame of it, which is that each divider
// is an edge of a pane the frame is actually drawing.

// transitionOS builds an auto-tiling session of n panes with animations on, and
// restores the globals it moves when the test ends.
func transitionOS(t *testing.T, n int, shared, bsp bool) *OS {
	t.Helper()
	restoreTransitionConfig(t)
	config.SharedBorders = shared
	config.AnimationsEnabled = true

	return newTransitionOS(t, n, bsp)
}

func restoreTransitionConfig(t *testing.T) {
	t.Helper()
	shared, anim := config.SharedBorders, config.AnimationsEnabled
	dock, style, ascii := config.DockbarPosition, config.BorderStyle, config.UseASCIIOnly
	// The dividers meet the dock's hairline, and the ASCII set draws every
	// junction as "+", so both are part of the shape these tests read.
	config.DockbarPosition = "bottom"
	config.BorderStyle = "rounded"
	config.UseASCIIOnly = false
	t.Cleanup(func() {
		config.SharedBorders, config.AnimationsEnabled = shared, anim
		config.DockbarPosition, config.BorderStyle, config.UseASCIIOnly = dock, style, ascii
	})
}

func newTransitionOS(t *testing.T, n int, bsp bool) *OS {
	t.Helper()
	m := &OS{
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		WorkspaceTrees:   map[int]*layout.BSPTree{},
		WindowToBSPID:    map[string]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            80,
		Height:           24,
		AutoTiling:       true,
		UseBSPLayout:     true,
		MasterRatio:      0.5,
	}
	for i := range n {
		m.Windows = append(m.Windows, newTransitionWindow(t, i))
	}
	// Both modes build the BSP tree first: in the real app it is there from BSP
	// being the default, and it stays behind when the user switches away.
	m.TileAllWindows()
	settle(m)
	if !bsp {
		m.ApplyLayoutModeName(LayoutModeMasterStack)
		m.TileAllWindows()
		settle(m)
	}
	return m
}

func newTransitionWindow(t *testing.T, i int) *terminal.Window {
	t.Helper()
	win := newTestWindow(t, fmt.Sprintf("tw-%d", i), 40, 12)
	win.Workspace = 1
	return win
}

// settle runs every in-flight animation to completion.
func settle(m *OS) {
	for range 200 {
		if len(m.Animations) == 0 {
			return
		}
		seekTransition(m, 1)
	}
}

// seekTransition puts every in-flight animation at the given fraction of its
// duration. The engine reads the wall clock, so the start time is what moves.
func seekTransition(m *OS, f float64) {
	now := time.Now()
	for _, a := range m.Animations {
		a.StartTime = now.Add(-time.Duration(float64(a.Duration) * f))
	}
	m.UpdateAnimations()
}

// paintTransitionPanes gives each pane a screenful of its own letter, so a cell
// the grid takes from a pane is visible as the letter it replaced.
func paintTransitionPanes(m *OS) {
	for i, w := range m.Windows {
		var sb strings.Builder
		sb.WriteString("\x1b[H")
		for r := range 40 {
			if r > 0 {
				sb.WriteString("\r\n")
			}
			sb.WriteString(strings.Repeat(string(rune('a'+i)), 200))
		}
		w.LockIO()
		_, _ = w.Terminal.Write([]byte(sb.String()))
		w.UnlockIO()
		w.MarkContentDirty()
	}
}

func transitionFrame(m *OS) string {
	paintTransitionPanes(m)
	return ansi.Strip(lipgloss.Sprint(m.GetCanvas(true).Render()))
}

// transitionScenarios are the layout changes a pane animates through.
var transitionScenarios = []struct {
	name  string
	panes int
	run   func(t *testing.T, m *OS)
}{
	{"spawn-into-2", 2, spawnPane},
	{"spawn-into-4", 4, spawnPane},
	{"swap", 4, func(_ *testing.T, m *OS) {
		w := m.Windows[0]
		m.SwapWindowsWithOriginal(0, 3, w.X, w.Y, w.Width, w.Height)
	}},
	{"close", 4, func(_ *testing.T, m *OS) { m.DeleteWindow(1) }},
}

// spawnPane adds a pane the way AddWindow does, at the floating box a new window
// is placed at before the tiler snaps it into its slot.
func spawnPane(t *testing.T, m *OS) {
	t.Helper()
	win := newTransitionWindow(t, len(m.Windows))
	win.X, win.Y, win.Width, win.Height = 10, 4, 40, 12
	m.Windows = append(m.Windows, win)
	m.FocusWindow(len(m.Windows) - 1)
	if m.UseBSPLayout {
		m.AddWindowToBSPTree(win)
	} else {
		m.TileAllWindows()
	}
}

// transitionSteps are the points through a transition the frames are read at.
// The last one is the frame before the animation completes, which is what has to
// already be the settled frame for the transition not to end in a jump.
var transitionSteps = []float64{0, 0.2, 0.4, 0.6, 0.8, 0.999}

func forEachTransition(t *testing.T, body func(t *testing.T, m *OS, shared, bsp bool, run func(*testing.T, *OS))) {
	for _, shared := range []bool{true, false} {
		for _, bsp := range []bool{true, false} {
			for _, sc := range transitionScenarios {
				mode := "master"
				if bsp {
					mode = "bsp"
				}
				t.Run(fmt.Sprintf("shared=%v/%s/%s", shared, mode, sc.name), func(t *testing.T) {
					m := transitionOS(t, sc.panes, shared, bsp)
					body(t, m, shared, bsp, sc.run)
				})
			}
		}
	}
}

// gridCells renders the separator overlay and returns the cell each divider
// glyph lands on.
func gridCells(m *OS) map[[2]int]rune {
	cells := map[[2]int]rune{}
	for _, l := range m.renderSeparatorOverlay() {
		text := []rune(ansi.Strip(l.GetContent()))
		x, y := l.GetX(), l.GetY()
		for i, r := range text {
			cells[[2]int{x + i, y}] = r
		}
	}
	return cells
}

// onSomePaneEdge reports whether the cell is an edge of a pane that no pane
// above it covers there. That is the whole claim the grid makes on a frame in
// motion: every divider is a boundary of a pane being drawn, and it is only
// drawn where the pane it belongs to is the one in front.
func onSomePaneEdge(stack []layout.Rect, x, y int) bool {
	for depth, r := range stack {
		onRing := x >= r.X-1 && x <= r.X+r.W && y >= r.Y-1 && y <= r.Y+r.H &&
			(x == r.X-1 || x == r.X+r.W || y == r.Y-1 || y == r.Y+r.H)
		if !onRing {
			continue
		}
		covered := false
		for above := depth + 1; above < len(stack); above++ {
			a := stack[above]
			if x >= a.X && x < a.X+a.W && y >= a.Y && y < a.Y+a.H {
				covered = true
				break
			}
		}
		if !covered {
			return true
		}
	}
	return false
}

// TestTransitionDividersFollowThePanes is the regression for a transition drawn
// as panes sliding under a fixed skeleton. The grid used to come from the
// settled layout, so for the length of every animation it described where the
// panes were going while they were somewhere else.
func TestTransitionDividersFollowThePanes(t *testing.T) {
	forEachTransition(t, func(t *testing.T, m *OS, shared, _ bool, run func(*testing.T, *OS)) {
		if !shared {
			t.Skip("dividers are only drawn under shared borders")
		}
		run(t, m)
		bounds := m.GetBSPBounds()
		for _, step := range transitionSteps {
			seekTransition(m, step)
			stack := m.tiledPaneRects()
			for cell, ch := range gridCells(m) {
				x, y := cell[0], cell[1]
				// A divider carries on for one cell onto the chrome's rule, which
				// is outside the region the panes divide up.
				if y < bounds.Y || y >= bounds.Y+bounds.H || x < bounds.X || x >= bounds.X+bounds.W {
					continue
				}
				if !onSomePaneEdge(stack, x, y) {
					t.Fatalf("t=%.3f: %q at (%d,%d) is on no pane's edge; panes are at %v",
						step, string(ch), x, y, stack)
				}
			}
		}
	})
}

// TestTransitionKeepsOutOfGuestCells pins the invariant a separator broke once
// by eating a pane's first column. A divider may only land on a cell the pane it
// belongs to owns, which is the cell just outside its rectangle, and it stands
// down where a pane stacked above is drawing there.
func TestTransitionKeepsOutOfGuestCells(t *testing.T) {
	forEachTransition(t, func(t *testing.T, m *OS, shared, _ bool, run func(*testing.T, *OS)) {
		if !shared {
			t.Skip("dividers are only drawn under shared borders")
		}
		run(t, m)
		for _, step := range transitionSteps {
			seekTransition(m, step)
			stack := m.tiledPaneRects()
			for cell := range gridCells(m) {
				x, y := cell[0], cell[1]
				for depth, r := range stack {
					inside := x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
					if !inside {
						continue
					}
					// Being inside a pane is only allowed where a pane in front
					// of it owns the cell, the same way that pane's body covers
					// the one behind.
					if !onSomePaneEdge(stack[depth+1:], x, y) {
						t.Fatalf("t=%.3f: divider at (%d,%d) is inside pane %v with nothing in front of it",
							step, x, y, r)
					}
				}
			}
		}
	})
}

// TestTransitionLandsOnTheSettledGrid is what says the animation is a transition
// rather than a second renderer: the frame just before the animation completes
// already draws the grid the settled frame draws, cell for cell and glyph for
// glyph, so nothing pops when the last tick lands.
//
// It reads the grid rather than the whole frame because the panes' own contents
// are not settled on that frame by design: the emulator is resized once, when
// the transition ends, so a pane is still wrapping its output to the size it is
// leaving. That is the animation engine's decision and predates the grid.
func TestTransitionLandsOnTheSettledGrid(t *testing.T) {
	forEachTransition(t, func(t *testing.T, m *OS, shared, _ bool, run func(*testing.T, *OS)) {
		if !shared {
			t.Skip("dividers are only drawn under shared borders")
		}
		run(t, m)
		seekTransition(m, 0.999)
		last := gridCells(m)
		settle(m)
		settled := gridCells(m)
		for cell, want := range settled {
			if got, ok := last[cell]; !ok || got != want {
				t.Fatalf("(%d,%d) is %q on the settled frame and %q on the frame before it",
					cell[0], cell[1], string(want), string(got))
			}
		}
		for cell, got := range last {
			if _, ok := settled[cell]; !ok {
				t.Fatalf("(%d,%d) carries %q on the frame before the animation completes and nothing after it",
					cell[0], cell[1], string(got))
			}
		}
	})
}

// TestTransitionSettlesWhereNoAnimationWould is the other half of that: the
// layout the panes travel to is the one the same session reaches with animations
// off, so turning them on changes the journey and nothing else.
func TestTransitionSettlesWhereNoAnimationWould(t *testing.T) {
	for _, shared := range []bool{true, false} {
		for _, bsp := range []bool{true, false} {
			for _, sc := range transitionScenarios {
				mode := "master"
				if bsp {
					mode = "bsp"
				}
				t.Run(fmt.Sprintf("shared=%v/%s/%s", shared, mode, sc.name), func(t *testing.T) {
					restoreTransitionConfig(t)
					config.SharedBorders = shared

					config.AnimationsEnabled = true
					animated := newTransitionOS(t, sc.panes, bsp)
					sc.run(t, animated)
					settle(animated)

					config.AnimationsEnabled = false
					still := newTransitionOS(t, sc.panes, bsp)
					sc.run(t, still)

					if got, want := transitionFrame(animated), transitionFrame(still); got != want {
						t.Errorf("the settled frame differs from the one animations-off produces\nanimated:\n%s\nstill:\n%s", got, want)
					}
				})
			}
		}
	}
}
