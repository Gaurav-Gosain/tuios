package app

import (
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

func withButtonStyle(t *testing.T, style string, fn func()) {
	t.Helper()
	prev := config.Global.WindowButtonStyle
	config.Global.WindowButtonStyle = style
	defer func() { config.Global.WindowButtonStyle = prev }()
	fn()
}

// drawTopBorder renders a window's frame and returns the visible cells of its
// top row, with the recorded controls.
func drawTopBorder(t *testing.T, m *OS, win *terminal.Window, tiling bool) ([]rune, []WindowButtonRect) {
	t.Helper()
	content := strings.Repeat(" ", win.Width)
	out := m.addToBorder(content, lipgloss.Width(content)-2, lipgloss.Color("#7dd3fc"), win, 1, tiling)
	top, _, _ := strings.Cut(out, "\n")
	return []rune(ansi.Strip(top)), m.windowButtonRects[win.ID]
}

// Floating panes overlap, so one pane's title bar can land on another pane's
// controls. The press belongs to the pane the click resolved to, and asking by
// window is what keeps that from depending on which entry a map handed back
// first.
func TestOverlappingControlsResolveToTheirOwnWindow(t *testing.T) {
	withButtonStyle(t, config.WindowButtonStyleDots, func() {
		under := &terminal.Window{ID: "under", X: 0, Y: 5, Width: 40, Height: 8, Workspace: 1}
		over := &terminal.Window{ID: "over", X: 0, Y: 5, Width: 40, Height: 8, Workspace: 1}
		m := &OS{Settings: config.Global, Windows: []*terminal.Window{under, over}}
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

// buttonLayoutSettings is the shipped settings with the controls set to one
// style and one end, and the title on the top bar so it competes with them.
func buttonLayoutSettings(style, position string) config.Settings {
	s := config.DefaultSettings()
	s.WindowButtonStyle = style
	s.WindowButtonPosition = position
	s.WindowTitlePosition = "top"
	s.HideWindowButtons = false
	return s
}

// wantControls is the run of cells the controls should take, and the action
// of each control from left to right.
func wantControls(m *OS, win *terminal.Window, tiling bool) (string, []WindowButtonAction) {
	s := &m.Settings
	zoom := windowButtonsHaveZoom(tiling, s)
	if s.WindowButtonStyle == config.WindowButtonStyleDots {
		// The traffic light is close, minimize, zoom at either end.
		order := []WindowButtonAction{WindowButtonClose, WindowButtonMinimize}
		if zoom {
			order = append(order, WindowButtonZoom)
		}
		text := " "
		for _, a := range order {
			text += markAt(m, win, a) + " "
		}
		return text, order
	}
	// The pill keeps close at the outer corner: minimize, zoom, close on the
	// right, mirrored on the left.
	order := []WindowButtonAction{WindowButtonMinimize}
	if zoom {
		order = append(order, WindowButtonZoom)
	}
	order = append(order, WindowButtonClose)
	if s.WindowButtonPosition == config.WindowButtonPositionLeft {
		slices.Reverse(order)
	}
	text := s.GetWindowPillLeft() + " "
	for _, a := range order {
		text += markAt(m, win, a) + " "
	}
	return text + s.GetWindowPillRight(), order
}

// markAt is the glyph a control is drawn as on this frame.
func markAt(m *OS, win *terminal.Window, a WindowButtonAction) string {
	s := &m.Settings
	if s.WindowButtonStyle == config.WindowButtonStyleDots {
		if m.windowButtonHover == win.ID {
			return windowDotSymbol(a, s)
		}
		return s.GetWindowButtonDot()
	}
	switch a {
	case WindowButtonClose:
		return s.GetWindowButtonCloseMark()
	case WindowButtonZoom:
		return s.GetWindowButtonMaximizeMark()
	default:
		return s.GetWindowButtonMinimizeMark()
	}
}

// TestWindowButtonGoldenBorders pins the whole top border for the default and
// the ASCII glyphs, so a change to the spacing or the order shows up as text.
func TestWindowButtonGoldenBorders(t *testing.T) {
	theme.SetActiveGlyphs(theme.GlyphSetNone)
	const (
		l = ""
		r = ""
	)
	cases := []struct {
		name, style, position string
		ascii                 bool
		want                  string
		order                 []WindowButtonAction
	}{
		{"pill-left", "pill", "left", false, "╭" + l + " ✕ □ - " + r + "──────────╮",
			[]WindowButtonAction{WindowButtonClose, WindowButtonZoom, WindowButtonMinimize}},
		{"pill-right", "pill", "right", false, "╭──────────" + l + " - □ ✕ " + r + "╮",
			[]WindowButtonAction{WindowButtonMinimize, WindowButtonZoom, WindowButtonClose}},
		{"dots-left", "dots", "left", false, "╭ ● ● ● ────────────╮",
			[]WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom}},
		{"dots-right", "dots", "right", false, "╭──────────── ● ● ● ╮",
			[]WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom}},
		{"pill-left-ascii", "pill", "left", true, "+[ X O - ]----------+",
			[]WindowButtonAction{WindowButtonClose, WindowButtonZoom, WindowButtonMinimize}},
		{"pill-right-ascii", "pill", "right", true, "+----------[ - O X ]+",
			[]WindowButtonAction{WindowButtonMinimize, WindowButtonZoom, WindowButtonClose}},
		{"dots-left-ascii", "dots", "left", true, "+ o o o ------------+",
			[]WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom}},
		{"dots-right-ascii", "dots", "right", true, "+------------ o o o +",
			[]WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := buttonLayoutSettings(c.style, c.position)
			s.UseASCIIOnly = c.ascii
			m := &OS{Settings: s}
			win := &terminal.Window{ID: "w", X: 10, Y: 4, Width: 21, Height: 6, Workspace: 1}
			row, rects := drawTopBorder(t, m, win, false)
			if got := string(row); got != c.want {
				t.Errorf("top border\n got %q\nwant %q", got, c.want)
			}
			if len(rects) != len(c.order) {
				t.Fatalf("recorded %d controls, want %d", len(rects), len(c.order))
			}
			for i, rect := range rects {
				if rect.Action != c.order[i] {
					t.Errorf("control %d is %v, want %v", i, rect.Action, c.order[i])
				}
				if got, want := string(row[rect.X-win.X]), markAt(m, win, rect.Action); got != want {
					t.Errorf("control %v starts on %q, want its mark %q", rect.Action, got, want)
				}
			}
		})
	}
}

// TestWindowButtonHitZonesFollowTheGlyphs draws every style at both ends in
// every glyph set, tiled and floating, with and without zoom, at rest and
// hovered, over a sweep of widths down to one the controls do not fit.
// Wherever the controls are drawn, each recorded rectangle must start on its
// own mark, cover the cell after it, sit on the title row, and follow the drawn
// order.
func TestWindowButtonHitZonesFollowTheGlyphs(t *testing.T) {
	t.Cleanup(func() { theme.SetActiveGlyphs(theme.GlyphSetNone) })
	onOff := func(b bool, on, off string) string {
		if b {
			return on
		}
		return off
	}
	sets := []string{theme.GlyphSetNone, "unicode", "heavy", "ascii", "ascii-only"}
	for _, set := range sets {
		for _, style := range config.WindowButtonStyles {
			for _, position := range config.WindowButtonPositions {
				for _, tiling := range []bool{false, true} {
					for _, zoom := range []bool{true, false} {
						for _, hovered := range []bool{false, true} {
							name := strings.Join([]string{set, style, position,
								onOff(tiling, "tiled", "floating"),
								onOff(zoom, "zoom", "nozoom"),
								onOff(hovered, "hover", "rest")}, "/")
							t.Run(name, func(t *testing.T) {
								if set == "ascii-only" {
									theme.SetActiveGlyphs(theme.GlyphSetNone)
								} else {
									theme.SetActiveGlyphs(set)
								}
								s := buttonLayoutSettings(style, position)
								s.UseASCIIOnly = set == "ascii-only"
								s.WindowButtonZoom = zoom
								checkButtonLayoutWidths(t, s, tiling, hovered)
							})
						}
					}
				}
			}
		}
	}
}

func checkButtonLayoutWidths(t *testing.T, s config.Settings, tiling, hovered bool) {
	t.Helper()
	for width := 3; width <= 40; width++ {
		m := &OS{Settings: s}
		win := &terminal.Window{ID: "w", X: 7, Y: 3, Width: width, Height: 6, Workspace: 1, CustomName: "a long shell title"}
		if hovered {
			m.windowButtonHover = win.ID
		}
		row, rects := drawTopBorder(t, m, win, tiling)
		want, order := wantControls(m, win, tiling)
		wantW := len([]rune(want))
		if width-2 < wantW {
			// A bar the controls do not fit comes back empty, and must record
			// nothing a press could land on.
			if len(rects) != 0 {
				t.Errorf("width %d: controls recorded on a bar they do not fit", width)
			}
			continue
		}
		if len(row) != width {
			t.Fatalf("width %d: the top border is %d cells", width, len(row))
		}
		// The controls sit against the corner at their end, and are the same
		// run of cells at either end apart from the order.
		var got string
		if s.WindowButtonPosition == config.WindowButtonPositionLeft {
			got = string(row[1 : 1+wantW])
		} else {
			got = string(row[width-1-wantW : width-1])
		}
		if got != want {
			t.Fatalf("width %d: controls drawn as %q, want %q (row %q)", width, got, want, string(row))
		}
		if len(rects) != len(order) {
			t.Fatalf("width %d: recorded %d controls, want %d", width, len(rects), len(order))
		}
		for i, rect := range rects {
			if rect.Action != order[i] {
				t.Errorf("width %d: control %d is %v, want %v", width, i, rect.Action, order[i])
			}
			if rect.Y != win.Y || rect.W != 2 {
				t.Errorf("width %d: control %v is at row %d width %d, want row %d width 2", width, rect.Action, rect.Y, rect.W, win.Y)
			}
			col := rect.X - win.X
			if col < 1 || col+rect.W > width-1 {
				t.Fatalf("width %d: control %v spans columns %d-%d, off the bar", width, rect.Action, col, col+rect.W-1)
			}
			if g, w := string(row[col]), markAt(m, win, rect.Action); g != w {
				t.Errorf("width %d: control %v starts on %q, want its mark %q (row %q)", width, rect.Action, g, w, string(row))
			}
			if row[col+1] != ' ' {
				t.Errorf("width %d: control %v's second cell is %q, want the space after its mark", width, rect.Action, string(row[col+1]))
			}
			if i > 0 && rects[i-1].X+rects[i-1].W != rect.X {
				t.Errorf("width %d: controls %v and %v are not adjacent", width, rects[i-1].Action, rect.Action)
			}
		}
	}
}
