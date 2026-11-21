package app

import (
	"image/color"
	"slices"
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

// The controls a title bar can carry.
const (
	WindowButtonNone WindowButtonAction = iota
	WindowButtonClose
	WindowButtonMinimize
	WindowButtonZoom
	// WindowButtonFloat moves a TML surface between a popup and a tiled
	// pane. Only a TML window draws it: see tml_place.go.
	WindowButtonFloat
)

func windowButtonsHaveZoom(isTiling bool, s *config.Settings) bool {
	if !isTiling {
		return true
	}
	return s.WindowButtonZoom
}

// WindowButtonRect is where one control was drawn on the last frame: the row,
// the span of columns it took, and what pressing it does.
//
// It is recorded by the renderer as it lays the controls out, because only the
// renderer knows how wide each one came out and which end it put them on. Every
// style draws a different set of glyphs at a different width, the pill goes to
// whichever end appearance.window_button_position names, and the corner glyph
// it stops short of is itself configurable, so a handler that derived these
// from the window rectangle would be re-deriving the layout from constants the
// layout does not read. The offsets it used to derive them from had already
// drifted one column.
type WindowButtonRect struct {
	Action WindowButtonAction
	X, W   int
	Y      int
}

// Contains reports whether a press at (x, y) landed on the control.
func (r WindowButtonRect) Contains(x, y int) bool {
	return y == r.Y && x >= r.X && x < r.X+r.W
}

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

func (m *OS) pruneWindowButtonRects() {
	if len(m.windowButtonRects) == 0 {
		return
	}
	for id := range m.windowButtonRects {
		if m.windowByID(id) == nil && m.sidebarSessionWindowByID(id) == nil {
			delete(m.windowButtonRects, id)
		}
	}
}

// WindowButtonIn returns the control one window drew at (x, y) on the last
// frame. The click handler asks by window because the click has already been
// resolved to one: floating panes overlap, so one pane's title bar can sit over
// another pane's controls, and the press belongs to whichever pane the frame
// put on top.
func (m *OS) WindowButtonIn(windowID string, x, y int) (WindowButtonAction, bool) {
	for _, r := range m.windowButtonRects[windowID] {
		if r.Contains(x, y) {
			return r.Action, true
		}
	}
	return WindowButtonNone, false
}

// WindowButtonAt returns the control drawn at (x, y) on the last frame, and
// which window owns it. Windows are walked in their own order rather than the
// map's, so an overlap resolves the same way twice running.
func (m *OS) WindowButtonAt(x, y int) (windowID string, action WindowButtonAction, ok bool) {
	for _, side := range []sidebarEdge{sidebarLeft, sidebarRight} {
		if !m.sidebarSessionVisible(side) {
			continue
		}
		v := &m.sidebarSessions[side]
		for _, pane := range v.panes {
			if w := m.sidebarSessionWindowByID(pane.ID); w != nil {
				if action, ok := m.WindowButtonIn(w.ID, x, y); ok {
					return w.ID, action, true
				}
			}
		}
	}
	for _, w := range m.Windows {
		if action, ok := m.WindowButtonIn(w.ID, x, y); ok {
			return w.ID, action, true
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
		} else if w := m.sidebarSessionWindowByID(wid); w != nil {
			w.Dirty = true
			m.MarkAllDirty()
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

type windowButtonPiece struct {
	action WindowButtonAction
	text   string
}

func (m *OS) buildWindowButtons(col color.Color, window *terminal.Window, isTiling bool) (string, []WindowButtonRect) {
	if m.Settings.HideWindowButtons {
		return "", nil
	}

	var pieces []windowButtonPiece
	if m.Settings.WindowButtonStyle == config.WindowButtonStyleDots {
		pieces = m.windowDotPieces(col, window, isTiling)
	} else {
		pieces = windowPillPieces(col, isTiling, &m.Settings)
	}

	if m.DrawsTML(window) && !window.IsPopup {
		pieces = append([]windowButtonPiece{{WindowButtonNone, " "}, {WindowButtonFloat, lipgloss.NewStyle().Foreground(col).Bold(true).Render("float")},
			{WindowButtonNone, " "}}, pieces...)
	}
	var pill strings.Builder
	var hits []WindowButtonRect
	offset := 0
	for _, p := range pieces {
		w := lipgloss.Width(p.text)
		if p.action != WindowButtonNone {
			hits = append(hits, WindowButtonRect{Action: p.action, X: offset, W: w})
		}
		pill.WriteString(p.text)
		offset += w
	}
	return pill.String(), hits
}

func windowPillPieces(col color.Color, isTiling bool, s *config.Settings) []windowButtonPiece {
	pillCap := lipgloss.NewStyle().Foreground(col).Render
	glyph := badgeStyle(col).Render

	type control struct {
		action WindowButtonAction
		mark   string
	}
	controls := []control{{WindowButtonMinimize, s.GetWindowButtonMinimizeMark()}}
	if windowButtonsHaveZoom(isTiling, s) {
		controls = append(controls, control{WindowButtonZoom, s.GetWindowButtonMaximizeMark()})
	}
	controls = append(controls, control{WindowButtonClose, s.GetWindowButtonCloseMark()})
	if s.WindowButtonPosition == config.WindowButtonPositionLeft {
		slices.Reverse(controls)
	}

	pieces := []windowButtonPiece{
		{WindowButtonNone, pillCap(s.GetWindowPillLeft())},
		{WindowButtonNone, glyph(" ")},
	}
	for _, c := range controls {
		pieces = append(pieces, windowButtonPiece{c.action, glyph(c.mark + " ")})
	}
	return append(pieces, windowButtonPiece{WindowButtonNone, pillCap(s.GetWindowPillRight())})
}

func (m *OS) windowDotPieces(col color.Color, window *terminal.Window, isTiling bool) []windowButtonPiece {
	ground := m.terminalBg()
	gap := lipgloss.NewStyle().Foreground(col).Render(" ")
	hovered := m.windowButtonHover == window.ID

	actions := []WindowButtonAction{WindowButtonClose, WindowButtonMinimize, WindowButtonZoom}
	if !windowButtonsHaveZoom(isTiling, &m.Settings) {
		actions = actions[:2]
	}

	pieces := []windowButtonPiece{{WindowButtonNone, gap}}
	for _, a := range actions {
		dot := readableDot(windowDotColor(a), ground)
		glyph := m.Settings.GetWindowButtonDot()
		if hovered {
			glyph = windowDotSymbol(a, &m.Settings)
		}
		cell := lipgloss.NewStyle().Foreground(dot).Render(glyph)
		pieces = append(pieces, windowButtonPiece{a, cell + gap})
	}
	return pieces
}

func placeWindowButtons(hits []WindowButtonRect, window *terminal.Window, pillStart int) []WindowButtonRect {
	if len(hits) == 0 || pillStart < 0 {
		return nil
	}
	start := window.X + pillStart
	out := make([]WindowButtonRect, len(hits))
	for i, h := range hits {
		out[i] = WindowButtonRect{Action: h.Action, X: start + h.X, W: h.W, Y: window.Y}
	}
	return out
}

const windowDotMinContrast = 3.0

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

func windowDotColor(action WindowButtonAction) color.Color {
	closeDot, minimise, zoom := theme.WindowDots()
	switch action {
	case WindowButtonClose:
		return closeDot
	case WindowButtonMinimize:
		return minimise
	default:
		return zoom
	}
}

func windowDotSymbol(action WindowButtonAction, s *config.Settings) string {
	if s.UseASCIIOnly {
		switch action {
		case WindowButtonClose:
			return "x"
		case WindowButtonMinimize:
			return "-"
		default:
			return "+"
		}
	}
	switch action {
	case WindowButtonClose:
		return "\u2297"
	case WindowButtonMinimize:
		return "\u2296"
	default:
		return "\u2295"
	}
}
