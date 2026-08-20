package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// The controls are hit-tested against rectangles the renderer records as it
// draws them, so what these pin is the agreement between the two: every
// recorded rectangle has to cover cells the pill really put ink on, and no
// cell outside it. That agreement is the whole reason the rectangles exist.
// The offsets the handler used to measure from the window rectangle had drifted
// a column, so a press on the corner glyph closed the window.

// withButtonStyle runs fn under one control style and restores the global.
func withButtonStyle(t *testing.T, style string, fn func()) {
	t.Helper()
	prev := config.WindowButtonStyle
	config.WindowButtonStyle = style
	defer func() { config.WindowButtonStyle = prev }()
	fn()
}

// withButtonPosition runs fn with the controls on one end of the bar and
// restores the global.
func withButtonPosition(t *testing.T, position string, fn func()) {
	t.Helper()
	prev := config.WindowButtonPosition
	config.WindowButtonPosition = position
	defer func() { config.WindowButtonPosition = prev }()
	fn()
}

// drawTopBorder renders a window's frame and returns the visible cells of its
// top row, with the recorded controls.
func drawTopBorder(t *testing.T, m *OS, win *terminal.Window, tiling bool) ([]rune, []WindowButtonRect) {
	t.Helper()
	content := strings.Repeat(" ", win.Width)
	out := m.addToBorder(content, lipgloss.Color("#7dd3fc"), win, 1, tiling)
	top, _, _ := strings.Cut(out, "\n")
	return []rune(ansi.Strip(top)), m.windowButtonRects[win.ID]
}

func TestWindowButtonRectsCoverTheGlyphsTheyWereDrawnFor(t *testing.T) {
	for _, style := range config.WindowButtonStyles {
		for _, position := range config.WindowButtonPositions {
			for _, tiling := range []bool{false, true} {
				for _, width := range []int{20, 40, 78, 200} {
					for _, originX := range []int{0, 7, 133} {
						withButtonPosition(t, position, func() {
							withButtonStyle(t, style, func() {
								win := &terminal.Window{ID: "w", X: originX, Y: 4, Width: width, Height: 10, Workspace: 1}
								m := &OS{Windows: []*terminal.Window{win}}
								cols, rects := drawTopBorder(t, m, win, tiling)

								wantControls := 3
								if tiling {
									wantControls = 2
								}
								if len(rects) != wantControls {
									t.Fatalf("%s/%s tiling=%v width=%d: recorded %d controls, want %d",
										style, position, tiling, width, len(rects), wantControls)
								}

								seen := map[WindowButtonAction]bool{}
								for _, r := range rects {
									if seen[r.Action] {
										t.Fatalf("%s/%s: action %v recorded twice", style, position, r.Action)
									}
									seen[r.Action] = true
									if r.Y != win.Y {
										t.Errorf("%s/%s: %v recorded on row %d, want the title row %d",
											style, position, r.Action, r.Y, win.Y)
									}
									// Every recorded column has to exist in the
									// row that was drawn, and both corner cells
									// are border glyphs no control may claim.
									first, last := win.X+1, win.X+len(cols)-1
									if r.X < first || r.X+r.W > last {
										t.Errorf("%s/%s tiling=%v width=%d: %v spans [%d,%d), outside the drawn row [%d,%d)",
											style, position, tiling, width, r.Action, r.X, r.X+r.W, first, last)
										return
									}
									// And it has to contain the glyph it stands
									// for: a span of nothing but padding is a
									// control the user cannot see but can press.
									ink := false
									for x := r.X; x < r.X+r.W; x++ {
										if cols[x-win.X] != ' ' {
											ink = true
										}
									}
									if !ink {
										t.Errorf("%s/%s tiling=%v width=%d: %v spans [%d,%d), which is blank",
											style, position, tiling, width, r.Action, r.X, r.X+r.W)
									}
								}
							})
						})
					}
				}
			}
		}
	}
}

// The pill goes flush against the corner at whichever end the setting names,
// and the hit rectangles go with it.
//
// The expected columns are worked out here from the pill's own width rather
// than read back off the drawn row, so a drift that moved the ink and the
// rectangles together by the same amount still fails. That is the one thing the
// agreement checks above cannot see.
func TestWindowButtonsSitAgainstTheEndTheyWereAskedFor(t *testing.T) {
	for _, style := range config.WindowButtonStyles {
		for _, position := range config.WindowButtonPositions {
			for _, tiling := range []bool{false, true} {
				withButtonPosition(t, position, func() {
					withButtonStyle(t, style, func() {
						win := &terminal.Window{ID: "w", X: 12, Y: 3, Width: 50, Height: 10, Workspace: 1}
						m := &OS{Windows: []*terminal.Window{win}}
						pill, pillHits := m.buildWindowButtons(lipgloss.Color("#7dd3fc"), win, tiling)
						pillWidth := lipgloss.Width(pill)

						cols, rects := drawTopBorder(t, m, win, tiling)

						// One cell of corner at each end of the row.
						want := win.X + 1
						if position == config.WindowButtonPositionRight {
							want = win.X + len(cols) - 1 - pillWidth
						}

						if len(rects) != len(pillHits) {
							t.Fatalf("%s/%s tiling=%v: drew %d controls but recorded %d",
								style, position, tiling, len(pillHits), len(rects))
						}
						for i, h := range pillHits {
							got := rects[i]
							if got.Action != h.Action || got.X != want+h.X || got.W != h.W {
								t.Errorf("%s/%s tiling=%v: %v recorded at column %d width %d, want %v at %d width %d",
									style, position, tiling, got.Action, got.X, got.W,
									h.Action, want+h.X, h.W)
							}
						}
					})
				})
			}
		}
	}
}

// The controls may not overlap, because a press has to resolve to one of them.
func TestWindowButtonRectsDoNotOverlap(t *testing.T) {
	for _, style := range config.WindowButtonStyles {
		for _, position := range config.WindowButtonPositions {
			withButtonPosition(t, position, func() {
				withButtonStyle(t, style, func() {
					win := &terminal.Window{ID: "w", X: 3, Y: 1, Width: 60, Height: 10, Workspace: 1}
					m := &OS{Windows: []*terminal.Window{win}}
					_, rects := drawTopBorder(t, m, win, false)
					for i, a := range rects {
						for _, b := range rects[i+1:] {
							if a.X < b.X+b.W && b.X < a.X+a.W {
								t.Errorf("%s/%s: %v [%d,%d) overlaps %v [%d,%d)",
									style, position, a.Action, a.X, a.X+a.W, b.Action, b.X, b.X+b.W)
							}
						}
					}
				})
			})
		}
	}
}

// The pill style's recorded columns contain the ones the button-position
// constants document, which is what ties the drawn layout, the constants and
// the hit test to one another. Containment rather than equality because the
// minimize glyph's run is four cells and the constants name only its last
// three; a recorded span is the whole run, padding included, so the button has
// no dead cell in it.
func TestPillRectsMatchTheDocumentedOffsets(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStylePill, func() {
		for _, tc := range []struct {
			tiling              bool
			action              WindowButtonAction
			wantLeft, wantRight int
		}{
			{false, WindowButtonClose, config.CloseButtonLeft, config.CloseButtonRight},
			{true, WindowButtonClose, config.CloseButtonLeft, config.CloseButtonRight},
			{false, WindowButtonZoom, config.MaximizeButtonLeft, config.MaximizeButtonRight},
			{true, WindowButtonMinimize, config.MinimizeButtonLeftTiling, config.MinimizeButtonRightTiling},
		} {
			win := &terminal.Window{ID: "w", X: 11, Y: 2, Width: 50, Height: 10, Workspace: 1}
			m := &OS{Windows: []*terminal.Window{win}}
			cols, rects := drawTopBorder(t, m, win, tc.tiling)
			end := win.X + len(cols) // one past the corner, which offset -1 names

			var got WindowButtonRect
			for _, r := range rects {
				if r.Action == tc.action {
					got = r
				}
			}
			left, right := got.X-end, got.X+got.W-1-end
			if left > tc.wantLeft || right < tc.wantRight {
				t.Errorf("tiling=%v %v was recorded on offsets %d..%d, which does not contain the documented %d..%d",
					tc.tiling, tc.action, left, right, tc.wantLeft, tc.wantRight)
			}
			// And it may not spill past the documented span into the next
			// control or the corner, which is the drift these replaced.
			if left < tc.wantLeft-1 || right > tc.wantRight {
				t.Errorf("tiling=%v %v was recorded on offsets %d..%d, wider than the %d..%d it was drawn on",
					tc.tiling, tc.action, left, right, tc.wantLeft, tc.wantRight)
			}
		}
	})
}

// A window with no room for the pill records nothing, so the cells it gave back
// to the frame cannot be pressed.
func TestWindowButtonRectsAreEmptyWhenNothingWasDrawn(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStylePill, func() {
		win := &terminal.Window{ID: "w", X: 0, Y: 0, Width: 6, Height: 4, Workspace: 1}
		m := &OS{Windows: []*terminal.Window{win}}
		if _, rects := drawTopBorder(t, m, win, false); len(rects) != 0 {
			t.Errorf("a %d-column window recorded %d controls, want none", win.Width, len(rects))
		}
	})

	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		prev := config.HideWindowButtons
		config.HideWindowButtons = true
		defer func() { config.HideWindowButtons = prev }()

		win := &terminal.Window{ID: "w", X: 0, Y: 0, Width: 60, Height: 8, Workspace: 1}
		m := &OS{Windows: []*terminal.Window{win}}
		if _, rects := drawTopBorder(t, m, win, false); len(rects) != 0 {
			t.Errorf("hidden buttons recorded %d controls, want none", len(rects))
		}
	})
}

// A control that has scrolled out from under the pointer stops being pressable
// with the window it belonged to.
func TestWindowButtonRectsAreDroppedWithTheirWindow(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		win := &terminal.Window{ID: "w", X: 4, Y: 3, Width: 40, Height: 8, Workspace: 1}
		m := &OS{Windows: []*terminal.Window{win}}
		_, rects := drawTopBorder(t, m, win, false)
		if len(rects) == 0 {
			t.Fatal("nothing was recorded to drop")
		}
		if _, _, ok := m.WindowButtonAt(rects[0].X, rects[0].Y); !ok {
			t.Fatal("the control that was just drawn is not hit-testable")
		}

		m.Windows = nil
		m.pruneWindowButtonRects()
		if _, _, ok := m.WindowButtonAt(rects[0].X, rects[0].Y); ok {
			t.Error("a closed window's controls are still pressable")
		}
	})
}

// Moving a window moves its controls with it, with no offset recomputed
// anywhere.
func TestWindowButtonRectsFollowTheWindow(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		win := &terminal.Window{ID: "w", X: 4, Y: 3, Width: 40, Height: 8, Workspace: 1}
		m := &OS{Windows: []*terminal.Window{win}}
		_, before := drawTopBorder(t, m, win, false)

		win.X += 9
		win.Y += 2
		_, after := drawTopBorder(t, m, win, false)

		for i := range before {
			if after[i].X-before[i].X != 9 || after[i].Y-before[i].Y != 2 {
				t.Errorf("%v moved by (%d,%d), want (9,2)",
					after[i].Action, after[i].X-before[i].X, after[i].Y-before[i].Y)
			}
		}
	})
}

// Floating panes overlap, so one pane's title bar can land on another pane's
// controls. The press belongs to the pane the click resolved to, and asking by
// window is what keeps that from depending on which entry a map handed back
// first.
func TestOverlappingControlsResolveToTheirOwnWindow(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		under := &terminal.Window{ID: "under", X: 0, Y: 5, Width: 40, Height: 8, Workspace: 1}
		over := &terminal.Window{ID: "over", X: 0, Y: 5, Width: 40, Height: 8, Workspace: 1}
		m := &OS{Windows: []*terminal.Window{under, over}}
		_, underRects := drawTopBorder(t, m, under, false)
		drawTopBorder(t, m, over, false)

		hit := underRects[0]
		if _, ok := m.WindowButtonIn("over", hit.X, hit.Y); !ok {
			t.Fatal("the pane on top does not own the cell its own control was drawn on")
		}
		if _, ok := m.WindowButtonIn("under", hit.X, hit.Y); !ok {
			t.Fatal("the pane underneath lost the control it drew")
		}
		if _, ok := m.WindowButtonIn("nosuchwindow", hit.X, hit.Y); ok {
			t.Error("a window that drew nothing was handed another window's control")
		}
	})
}

// The dots are unlabelled, so hovering them is what says what they do. All
// three reveal together, the way macOS does, and the reveal costs no width.
func TestDotsRevealTheirSymbolsOnHover(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		win := &terminal.Window{ID: "w", X: 2, Y: 1, Width: 40, Height: 8, Workspace: 1}
		m := &OS{Windows: []*terminal.Window{win}}

		idle, rects := drawTopBorder(t, m, win, false)
		dot := []rune(config.GetWindowButtonDot())[0]
		if strings.Count(string(idle), string(dot)) != 3 {
			t.Fatalf("idle bar drew %q, want three %c", string(idle), dot)
		}

		if !m.WindowButtonHoverAt(rects[1].X, rects[1].Y) {
			t.Fatal("hovering the middle control did not change the hover")
		}
		if !win.Dirty {
			t.Error("the window whose controls gained the hover was not marked for redraw")
		}
		hovered, _ := drawTopBorder(t, m, win, false)
		if len(hovered) != len(idle) {
			t.Errorf("hover changed the bar from %d cells to %d", len(idle), len(hovered))
		}
		if strings.ContainsRune(string(hovered), dot) {
			t.Errorf("hovered bar still shows a disc: %q", string(hovered))
		}
		// The circled forms, so a hovered control stays the same round shape
		// carrying a mark rather than becoming a filled block.
		for _, want := range []string{"\u2297", "\u2296", "\u2295"} {
			if !strings.Contains(string(hovered), want) {
				t.Errorf("hovered bar %q is missing %q; macOS reveals all three at once", string(hovered), want)
			}
		}

		if !m.WindowButtonHoverAt(0, 0) {
			t.Fatal("moving off the controls did not clear the hover")
		}
		if m.WindowButtonHoverActive() {
			t.Error("the hover survived the pointer leaving")
		}
	})
}

// The traffic lights are tuned for macOS's light-grey bar. They have to stay
// telling apart on whatever ground the theme puts behind them, which for the
// yellow on a near-white pane means giving up some of its brightness.
func TestDotsClearTheContrastFloorOnEveryGround(t *testing.T) {
	for _, id := range []string{"", "github", "tokyo_night", "builtin_solarized_light"} {
		if err := theme.Initialize(id); err != nil {
			t.Fatalf("theme %q: %v", id, err)
		}
		ground := theme.TerminalBg()
		for _, action := range []WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom} {
			ink := readableDot(windowDotColor(action), ground)
			if got := theme.ContrastRatio(ink, ground); got < windowDotMinContrast {
				t.Errorf("theme %q: %v measures %.2f against the pane, want at least %.2f",
					id, action, got, windowDotMinContrast)
			}
		}
	}
	_ = theme.Initialize("")
}
