package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// WindowButtonAction is what pressing one of a window's title-bar controls
// does. The renderer records it beside the cells it drew the control on, so the
// click handler never has to know which control sits where.
type WindowButtonAction int

// The controls a title bar can carry. A tiled window has no zoom, so its bar
// carries two.
const (
	WindowButtonNone WindowButtonAction = iota
	WindowButtonClose
	WindowButtonMinimize
	WindowButtonZoom
)

// WindowButtonRect is where one control was drawn on the last frame: the row,
// the span of columns it took, and what pressing it does.
//
// It is recorded by the renderer as it lays the controls out, because only the
// renderer knows how wide each one came out. Every style draws a different set
// of glyphs at a different width, and the pill is right-aligned against a
// border whose corner glyph is itself configurable, so a handler that derived
// these from the window rectangle would be re-deriving the layout from
// constants that the layout does not read. The offsets it used to derive them
// from had already drifted one column.
type WindowButtonRect struct {
	Action WindowButtonAction
	X, W   int
	Y      int
}

// Contains reports whether a press at (x, y) landed on the control.
func (r WindowButtonRect) Contains(x, y int) bool {
	return y == r.Y && x >= r.X && x < r.X+r.W
}

// recordWindowButtons stores the controls drawn for one window, replacing
// whatever the previous frame drew for it. A window whose bar drew none (hidden
// buttons, no room, no border) records an empty set, which is what stops a
// press landing on cells the frame gave back to the guest.
func (m *OS) recordWindowButtons(windowID string, rects []WindowButtonRect) {
	if m.windowButtonRects == nil {
		m.windowButtonRects = make(map[string][]WindowButtonRect, len(m.Windows))
	}
	if len(rects) == 0 {
		delete(m.windowButtonRects, windowID)
		return
	}
	m.windowButtonRects[windowID] = rects
}

// pruneWindowButtonRects drops the controls of windows that no longer exist.
//
// The set is not cleared per frame the way the scrollbar's is: a window whose
// layer was cached is composed without being redrawn, so its controls are still
// on screen and still have to be pressable. It is replaced whenever the window
// is redrawn, which is what a move or a resize forces.
func (m *OS) pruneWindowButtonRects() {
	if len(m.windowButtonRects) == 0 {
		return
	}
	for id := range m.windowButtonRects {
		if m.windowByID(id) == nil {
			delete(m.windowButtonRects, id)
		}
	}
}

// WindowButtonAt returns the control drawn at (x, y) on the last frame, and
// which window owns it.
func (m *OS) WindowButtonAt(x, y int) (windowID string, action WindowButtonAction, ok bool) {
	for id, rects := range m.windowButtonRects {
		for _, r := range rects {
			if r.Contains(x, y) {
				return id, r.Action, true
			}
		}
	}
	return "", WindowButtonNone, false
}

// WindowButtonHoverAt points the hover at whichever window's controls the
// pointer is over, and reports whether that changed. The dots style draws its
// symbols only while hovered, so a change has to redraw the window that gained
// the hover and the one that lost it.
func (m *OS) WindowButtonHoverAt(x, y int) bool {
	id, _, ok := m.WindowButtonAt(x, y)
	if !ok {
		id = ""
	}
	if id == m.windowButtonHover {
		return false
	}
	prev := m.windowButtonHover
	m.windowButtonHover = id
	for _, wid := range []string{prev, id} {
		if w := m.windowByID(wid); w != nil {
			w.Dirty = true
		}
	}
	return true
}

// WindowButtonHoverActive reports whether the pointer is currently on some
// window's controls. The motion whitelist in cmd/tuios reads it so one more
// event arrives after the pointer leaves, which is what clears the reveal.
func (m *OS) WindowButtonHoverActive() bool { return m.windowButtonHover != "" }

// WindowButtonContains reports whether (x, y) is on any window's controls, for
// the same whitelist.
func (m *OS) WindowButtonContains(x, y int) bool {
	_, _, ok := m.WindowButtonAt(x, y)
	return ok
}

// windowButtonPiece is one already-styled run of cells in the control pill. A
// piece with no action is decoration: a pill cap, or the gap that separates the
// dots from the border.
type windowButtonPiece struct {
	action WindowButtonAction
	text   string
}

// buildWindowButtons renders the window's control pill and reports, for each
// control, where in the pill it starts and how wide it came out. Nothing here
// counts cells by hand: the offsets fall out of the widths of the pieces that
// were actually rendered, so a style with different glyphs moves its own
// hitboxes with it.
func (m *OS) buildWindowButtons(col color.Color, window *terminal.Window, isTiling bool) (string, []WindowButtonRect) {
	if config.HideWindowButtons {
		return "", nil
	}

	var pieces []windowButtonPiece
	if config.WindowButtonStyle == config.WindowButtonStyleDots {
		pieces = m.windowDotPieces(col, window, isTiling)
	} else {
		pieces = windowPillPieces(col, isTiling)
	}

	var pill string
	var hits []WindowButtonRect
	offset := 0
	for _, p := range pieces {
		w := lipgloss.Width(p.text)
		if p.action != WindowButtonNone {
			hits = append(hits, WindowButtonRect{Action: p.action, X: offset, W: w})
		}
		pill += p.text
		offset += w
	}
	return pill, hits
}

// windowPillPieces is the original filled pill: black glyphs on the border
// colour, capped with powerline half circles, minimize then zoom then close.
func windowPillPieces(col color.Color, isTiling bool) []windowButtonPiece {
	pillCap := lipgloss.NewStyle().Foreground(col).Render
	glyph := baseButtonStyle.Background(col).Render

	pieces := []windowButtonPiece{
		{WindowButtonNone, pillCap(config.GetWindowPillLeft())},
		{WindowButtonMinimize, glyph("  - ")},
	}
	if !isTiling {
		pieces = append(pieces, windowButtonPiece{WindowButtonZoom, glyph(config.GetWindowButtonMaximize())})
	}
	return append(pieces,
		windowButtonPiece{WindowButtonClose, glyph(config.GetWindowButtonClose())},
		windowButtonPiece{WindowButtonNone, pillCap(config.GetWindowPillRight())},
	)
}

// windowDotPieces is the macOS traffic light: three unlabelled discs in red,
// yellow and green, close first, sitting straight on the border rather than in
// a pill of their own.
//
// Each control is the disc plus the gap after it, so the pill has no cell that
// belongs to nothing and a press between two dots resolves to the one on its
// left instead of falling through to the border. Two cells is also about as
// small as a pointer target should get.
//
// Hovering any of them turns all three into their symbols, which is what macOS
// does and what makes three unlabelled dots learnable. The symbol is drawn dark
// on the disc's own colour, so the cell keeps the shape and weight it had while
// idle and the pill cannot change width under the pointer.
func (m *OS) windowDotPieces(col color.Color, window *terminal.Window, isTiling bool) []windowButtonPiece {
	ground := theme.TerminalBg()
	gap := lipgloss.NewStyle().Foreground(col).Render(" ")
	hovered := m.windowButtonHover == window.ID

	actions := []WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom}
	if isTiling {
		actions = actions[:2]
	}

	pieces := []windowButtonPiece{{WindowButtonNone, gap}}
	for _, a := range actions {
		dot := readableDot(windowDotColor(a), ground)
		cell := lipgloss.NewStyle().Foreground(dot).Render(config.GetWindowButtonDot())
		if hovered {
			cell = lipgloss.NewStyle().
				Background(dot).
				Foreground(theme.ContrastText(dot)).
				Render(windowDotSymbol(a))
		}
		pieces = append(pieces, windowButtonPiece{a, cell + gap})
	}
	return pieces
}

// placeWindowButtons turns pill-relative spans into screen rectangles. The pill
// is right-aligned against the border's top-right corner on every path that
// draws it, and a border row that could not fit the pill is returned empty, so
// an empty row records nothing at all.
func placeWindowButtons(hits []WindowButtonRect, window *terminal.Window, topBorder, pill string) []WindowButtonRect {
	if len(hits) == 0 || topBorder == "" || pill == "" {
		return nil
	}
	rowWidth := lipgloss.Width(topBorder)
	if rowWidth < lipgloss.Width(pill) {
		return nil
	}
	// One cell in from the row's end is the corner glyph, which the pill stops
	// short of.
	start := window.X + rowWidth - 1 - lipgloss.Width(pill)
	out := make([]WindowButtonRect, len(hits))
	for i, h := range hits {
		out[i] = WindowButtonRect{Action: h.Action, X: start + h.X, W: h.W, Y: window.Y}
	}
	return out
}

// macOS traffic-light colours, as the maintainer asked for them. They are only
// a starting point: readableDot carries whichever of them misses the floor
// against the ground it lands on.
var (
	windowDotClose    = lipgloss.Color("#ff5f57")
	windowDotMinimize = lipgloss.Color("#febc2e")
	windowDotZoom     = lipgloss.Color("#28c840")
)

// windowDotMinContrast is the floor a dot has to clear against the ground it is
// drawn on: WCAG 2.1 SC 1.4.11, which asks 3:1 of a control that carries its
// meaning as a shape rather than as text. It is below theme.ContrastFloor for
// the same reason the scrollbar's is - nothing here has to be read - and above
// the scrollbar's, because these are targets to hit and not a readout.
const windowDotMinContrast = 3.0

// readableDot carries c toward the ground's own text colour until it clears
// windowDotMinContrast, and leaves it alone when it already does.
//
// The traffic-light colours are tuned for macOS's light-grey title bar, and
// they hold on a dark pane: measured on charmtone Pepper they are 5.5:1, 9.7:1
// and 7.3:1. On a near-white pane the yellow is the one that falls through,
// so buying luminance while keeping the hue is what keeps three dots reading as
// red, yellow and green on both.
func readableDot(c, ground color.Color) color.Color {
	if theme.ContrastRatio(c, ground) >= windowDotMinContrast {
		return c
	}
	target := theme.ContrastText(ground)
	const steps = 16
	for i := 1; i < steps; i++ {
		if mixed := blendColors(c, target, float64(i)/steps); theme.ContrastRatio(mixed, ground) >= windowDotMinContrast {
			return mixed
		}
	}
	return target
}

// windowDotColor returns the traffic-light colour for one action.
func windowDotColor(action WindowButtonAction) color.Color {
	switch action {
	case WindowButtonClose:
		return windowDotClose
	case WindowButtonMinimize:
		return windowDotMinimize
	default:
		return windowDotZoom
	}
}

// windowDotSymbol returns the glyph a hovered dot shows, the way macOS reveals
// its symbols when the pointer reaches the group. Each is one cell and each is
// already drawn elsewhere in the window chrome, so no new rune enters the
// frame and the ASCII forms come along for free.
func windowDotSymbol(action WindowButtonAction) string {
	switch action {
	case WindowButtonClose:
		return strings.TrimSpace(config.GetWindowButtonClose())
	case WindowButtonMinimize:
		return "-"
	default:
		return strings.TrimSpace(config.GetWindowButtonMaximize())
	}
}
