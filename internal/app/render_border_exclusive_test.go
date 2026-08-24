package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A pane edge is drawn by one of two systems: the pane's own border box
// (renderWindowBox, unless rendersBorderless says otherwise) or the shared
// separator overlay (renderSeparatorOverlay). They are alternatives, so a frame
// carrying both is the doubled-border fault.
//
// These assert against the composed frame rather than the layout, because the
// layout was already correct in the frame that first showed the fault: the
// rectangles had their separator gaps reserved and the panes drew boxes inside
// them anyway.

// borderModeOS builds an auto-tiling model of n windows in the given border
// mode. The windows start untiled, which is what a freshly created pane looks
// like and is the state that arms the deferral the transition test exercises.
func borderModeOS(t *testing.T, n int, shared, animations bool) *OS {
	t.Helper()
	origShared, origAnim := config.SharedBorders, config.AnimationsEnabled
	origStyle, origASCII := config.BorderStyle, config.UseASCIIOnly
	origSidebar := config.SidebarEnabled
	config.SharedBorders = shared
	config.AnimationsEnabled = animations
	// Other tests in this package mutate the style, and the ASCII set draws every
	// corner as "+", which would hide a pane box among the separator glyphs.
	config.BorderStyle = "rounded"
	config.UseASCIIOnly = false
	config.SidebarEnabled = false
	t.Cleanup(func() {
		config.SharedBorders = origShared
		config.AnimationsEnabled = origAnim
		config.BorderStyle = origStyle
		config.UseASCIIOnly = origASCII
		config.SidebarEnabled = origSidebar
	})

	m := &OS{
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:    config.SharedBorders,
		PaneGap:          config.PaneGap,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		AutoTiling:       true,
		UseBSPLayout:     true,
		FocusedWindow:    0,
	}
	for i := range n {
		w := benchWindow(t, fmt.Sprintf("border-%d-%d", n, i), 120, 40)
		w.Workspace = 1
		w.Width, w.Height = 120, 40
		m.Windows = append(m.Windows, w)
	}
	m.TileAllWindows()
	return m
}

// frameGrid composes the frame and returns it as rows of runes with the styling
// removed, so a cell can be read at a coordinate.
func frameGrid(t *testing.T, m *OS) [][]rune {
	t.Helper()
	plain := stripANSIForTrace(fmt.Sprint(m.GetCanvas(false).Render()))
	var g [][]rune
	for _, line := range strings.Split(plain, "\n") {
		g = append(g, []rune(line))
	}
	return g
}

func cellAt(g [][]rune, x, y int) rune {
	if y < 0 || y >= len(g) || x < 0 || x >= len(g[y]) {
		return ' '
	}
	return g[y][x]
}

// borderAudit counts, in the composed frame, the evidence each border system
// leaves behind.
//
// The three counts cannot be confused for one another. A pane's own box puts its
// top-left corner glyph exactly at the pane origin, a cell the overlay never
// touches because its focus ring sits one cell outside the rectangle. The
// overlay only draws in the gaps the shared layout reserves between rectangles,
// and the non-shared layout reserves none, so a glyph outside every rectangle
// came from the overlay. Junctions are overlay-only in either mode, since a box
// is a plain rectangle and has no arms to grow.
func borderAudit(t *testing.T, m *OS) (ownBoxes, strayRules, junctions int) {
	t.Helper()
	g := frameGrid(t, m)
	border := config.GetBorderForStyle()
	topLeft := firstRune(border.TopLeft, '╭')

	covered := make(map[[2]int]bool)
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace {
			continue
		}
		if cellAt(g, w.X, w.Y) == topLeft {
			ownBoxes++
		}
		for y := w.Y; y < w.Y+w.Height; y++ {
			for x := w.X; x < w.X+w.Width; x++ {
				covered[[2]int{x, y}] = true
			}
		}
	}

	ruleGlyphs := map[rune]bool{}
	for _, s := range []string{
		border.Top, border.Bottom, border.Left, border.Right,
		border.TopLeft, border.TopRight, border.BottomLeft, border.BottomRight,
	} {
		if r := firstRune(s, 0); r != 0 {
			ruleGlyphs[r] = true
		}
	}
	junctionGlyphs := map[rune]bool{}
	for _, s := range []string{
		border.Middle, border.MiddleLeft, border.MiddleRight,
		border.MiddleTop, border.MiddleBottom,
	} {
		if r := firstRune(s, 0); r != 0 {
			junctionGlyphs[r] = true
		}
	}

	b := m.GetBSPBounds()
	for y := b.Y; y < b.Y+b.H; y++ {
		for x := b.X; x < b.X+b.W; x++ {
			c := cellAt(g, x, y)
			if junctionGlyphs[c] {
				junctions++
			}
			if !covered[[2]int{x, y}] && (ruleGlyphs[c] || junctionGlyphs[c]) {
				strayRules++
			}
		}
	}
	return ownBoxes, strayRules, junctions
}

// TestBorderSystemsNeverDrawTogether pins the invariant at one, two and three
// panes, in both border modes, and both while a snap is in flight and after it
// has settled. The in-flight rows are the regression: tiling used to leave a
// pane's Tiled flag false until its animation completed, while the overlay read
// config.SharedBorders live, so for the length of the animation every pane drew
// a full box inside a layout that had already reserved separator gaps.
func TestBorderSystemsNeverDrawTogether(t *testing.T) {
	for _, panes := range []int{1, 2, 3} {
		for _, shared := range []bool{false, true} {
			for _, animating := range []bool{false, true} {
				name := fmt.Sprintf("panes-%d/shared-%t/animating-%t", panes, shared, animating)
				t.Run(name, func(t *testing.T) {
					m := borderModeOS(t, panes, shared, animating)
					ownBoxes, strayRules, junctions := borderAudit(t, m)

					if ownBoxes > 0 && strayRules > 0 {
						t.Errorf("both border systems drew: %d panes boxed and %d separator cells outside every pane",
							ownBoxes, strayRules)
					}

					if shared {
						if ownBoxes != 0 {
							t.Errorf("shared borders on: %d of %d panes still drew a box of their own",
								ownBoxes, panes)
						}
					} else {
						if ownBoxes != panes {
							t.Errorf("shared borders off: %d panes drew a box, want %d", ownBoxes, panes)
						}
						if strayRules != 0 {
							t.Errorf("shared borders off: %d separator cells drawn outside every pane", strayRules)
						}
						if junctions != 0 {
							t.Errorf("shared borders off: %d separator junction glyphs in the content region", junctions)
						}
					}
				})
			}
		}
	}
}

// TestSharedBorderToggleDoesNotDoubleBorders is the runtime transition that
// produced the reported frame. Turning shared borders on retiles, which snaps
// every pane to a gapped rectangle; anything that retires those snaps early left
// the panes bordered for good, because the flag they read was only ever going to
// be set by an animation that no longer existed. Starting a pane drag is the
// ordinary way to retire them.
func TestSharedBorderToggleDoesNotDoubleBorders(t *testing.T) {
	m := borderModeOS(t, 3, false, true)

	config.SharedBorders = true
	m.applyAppearanceLive(true)

	if len(m.Animations) == 0 {
		t.Fatal("toggling shared borders queued no snap animations, so the transition is untested")
	}

	t.Run("mid-snap", func(t *testing.T) {
		ownBoxes, _, _ := borderAudit(t, m)
		if ownBoxes != 0 {
			t.Errorf("mid-snap: %d panes drew a box while the separator overlay was live", ownBoxes)
		}
	})

	t.Run("snap-retired-by-drag", func(t *testing.T) {
		m.CompleteAllAnimations()
		ownBoxes, strayRules, _ := borderAudit(t, m)
		if ownBoxes != 0 {
			t.Errorf("after the snap was retired: %d panes drew a box, and %d separator cells were drawn outside every pane",
				ownBoxes, strayRules)
		}
		for _, w := range m.Windows {
			if !w.Tiled {
				t.Errorf("window %s left untiled while config.SharedBorders is true", w.ID)
			}
		}
	})
}
