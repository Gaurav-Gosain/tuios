package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// separatorSplits returns the divider lines for the layout that placed the
// panes. The two tilers reserve their gaps differently, so each has to answer
// for its own: the BSP tree knows its splits, and the master-stack tiler has
// them read back off the panes it positioned. Asking the tree while master-stack
// is driving is what used to paint a line down the pane beside it, because the
// tree survives the mode switch holding the geometry it last laid out.
func (m *OS) separatorSplits() []layout.SplitLine {
	if m.UseScrollingLayout {
		return nil
	}
	if !m.UseBSPLayout {
		return layout.SplitsBetween(m.tiledPaneRects(), 1)
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return nil
	}
	return tree.CollectSplits(m.GetBSPBounds())
}

// tiledPaneRects returns the rectangles of the panes currently tiled on screen.
func (m *OS) tiledPaneRects() []layout.Rect {
	rects := make([]layout.Rect, 0, len(m.Windows))
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.Minimizing || w.IsFloating || !w.Tiled {
			continue
		}
		rects = append(rects, layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height})
	}
	return rects
}

// joinSide names the side of the content region a divider runs into. A divider
// that reaches the region's edge carries on for one cell onto the rule the
// chrome draws there, so the two meet at a junction instead of the divider
// ending a cell short of the boundary it divides up to.
type joinSide uint8

const (
	joinNone joinSide = iota
	joinTop
	joinBottom
	joinLeft
	joinRight
)

// chromeRules gives the row or column of the rule that closes the content
// region on each side: the dock's hairline on the dock's side, the sidebar's
// edge rule on the sidebar's. A side the region shares with the screen edge has
// no rule and reports -1, since there is no cell beyond it to reach.
type chromeRules struct{ top, bottom, left, right int }

func (m *OS) chromeRules(bounds layout.Rect) chromeRules {
	r := chromeRules{top: -1, bottom: -1, left: -1, right: -1}
	switch config.DockbarPosition {
	case "hidden":
	case "top":
		r.top = bounds.Y - 1
	default:
		r.bottom = bounds.Y + bounds.H
	}
	if m.GetLeftMargin() > 0 {
		r.left = bounds.X - 1
	}
	if m.GetRightMargin() > 0 {
		r.right = bounds.X + bounds.W
	}
	return r
}

// renderSeparatorOverlay renders thin separator lines between tiled panes.
// Each separator line is its own lipgloss Layer to avoid occluding content.
func (m *OS) renderSeparatorOverlay() []*lipgloss.Layer {
	// Don't render shared borders when a window is zoomed
	if fw := m.GetFocusedWindow(); fw != nil && fw.Zoomed {
		return nil
	}

	bounds := m.GetBSPBounds()
	splits := m.separatorSplits()
	if len(splits) == 0 {
		return nil
	}

	viewW := m.GetRenderWidth()
	viewH := m.GetRenderHeight()

	// Collect all separator characters with positions
	type cell struct {
		vert, horiz bool
		join        joinSide
	}
	grid := make(map[[2]int]*cell)
	get := func(x, y int) *cell {
		k := [2]int{x, y}
		if c, ok := grid[k]; ok {
			return c
		}
		c := &cell{}
		grid[k] = c
		return c
	}

	// A divider that stops inside the region stops on another divider, and the
	// junction logic below draws that meeting. One that runs to the region's edge
	// has the chrome's rule to meet instead, one cell further out.
	rules := m.chromeRules(bounds)

	for _, s := range splits {
		if s.Vertical {
			if s.Pos < 0 || s.Pos >= viewW {
				continue
			}
			for y := max(s.From, 0); y <= min(s.To, viewH-1); y++ {
				get(s.Pos, y).vert = true
			}
			if s.From <= bounds.Y && rules.top >= 0 {
				get(s.Pos, rules.top).join = joinTop
			}
			if s.To >= bounds.Y+bounds.H-1 && rules.bottom >= 0 {
				get(s.Pos, rules.bottom).join = joinBottom
			}
		} else {
			if s.Pos < 0 || s.Pos >= viewH {
				continue
			}
			for x := max(s.From, 0); x <= min(s.To, viewW-1); x++ {
				get(x, s.Pos).horiz = true
			}
			if s.From <= bounds.X && rules.left >= 0 {
				get(rules.left, s.Pos).join = joinLeft
			}
			if s.To >= bounds.X+bounds.W-1 && rules.right >= 0 {
				get(rules.right, s.Pos).join = joinRight
			}
		}
	}

	// Get border characters from the configured style
	border := config.GetBorderForStyle()
	chVert := firstRune(border.Left, '│')
	chHoriz := firstRune(border.Top, '─')
	chCross := firstRune(border.Middle, '┼')
	chTRight := firstRune(border.MiddleLeft, '├') // ├ T pointing right
	chTLeft := firstRune(border.MiddleRight, '┤') // ┤ T pointing left
	chTDown := firstRune(border.MiddleTop, '┬')   // ┬ T pointing down
	chTUp := firstRune(border.MiddleBottom, '┴')  // ┴ T pointing up

	// The perimeter of the focused window, clipped to the tiled bounds. Cells on
	// it are drawn in the focus color, so the focused pane reads as an outlined
	// rectangle even though every segment is shared with a neighbour.
	focus := m.focusPerimeter(bounds)

	// Resolve each cell to a character
	type charPos struct {
		x, y    int
		ch      rune
		focused bool
		join    bool
	}
	var chars []charPos

	for k, c := range grid {
		x, y := k[0], k[1]
		var ch rune
		switch {
		case c.join == joinTop:
			ch = chTDown
		case c.join == joinBottom:
			ch = chTUp
		case c.join == joinLeft:
			ch = chTRight
		case c.join == joinRight:
			ch = chTLeft
		case c.vert && c.horiz:
			ch = chCross
		case c.vert:
			// Check horizontal neighbors for T-junctions
			_, hasL := grid[[2]int{x - 1, y}]
			_, hasR := grid[[2]int{x + 1, y}]
			switch {
			case hasL && hasR:
				ch = chCross
			case hasR:
				ch = chTRight
			case hasL:
				ch = chTLeft
			default:
				ch = chVert
			}
		case c.horiz:
			_, hasU := grid[[2]int{x, y - 1}]
			_, hasD := grid[[2]int{x, y + 1}]
			switch {
			case hasU && hasD:
				ch = chCross
			case hasD:
				ch = chTDown
			case hasU:
				ch = chTUp
			default:
				ch = chHoriz
			}
		default:
			continue
		}

		onFocus := focus.contains(x, y)
		// At a corner of the focused perimeter, bend the line into the focused
		// window. This is the only signal that is independent of color, and it
		// is what disambiguates two panes sharing a single divider: the divider
		// hooks toward whichever side owns it. Plain segments and the meeting
		// with a chrome rule are replaced, both being places the perimeter turns;
		// a crossing between two dividers keeps the arms its neighbours need.
		if onFocus && (ch == chVert || ch == chHoriz || c.join != joinNone) {
			if corner, ok := focus.corner(x, y, border); ok {
				ch = corner
			}
		}
		chars = append(chars, charPos{x, y, ch, onFocus, c.join != joinNone})
	}

	if len(chars) == 0 {
		return nil
	}

	// Build color strings. The focused perimeter is drawn bold as well as tinted
	// so the signal survives themes where the two border colors are close, and
	// so it is not carried by hue alone.
	unfocusedStr := sgrForeground(theme.BorderUnfocused())
	focusColor := theme.BorderFocusedWindow()
	if m.Mode == TerminalMode {
		focusColor = theme.BorderFocusedTerminal()
	}
	focusedStr := "\x1b[1m" + sgrForeground(focusColor)
	reset := "\x1b[0m"

	// Group into contiguous horizontal runs to minimize layer count.
	// A "run" is a sequence of chars on the same row with consecutive X positions
	// and the same focus state.
	type run struct {
		x, y int
		text string
		join bool
	}

	// Sort chars by (y, x) for grouping
	// Simple insertion sort since count is small
	for i := 1; i < len(chars); i++ {
		for j := i; j > 0; j-- {
			if chars[j].y < chars[j-1].y || (chars[j].y == chars[j-1].y && chars[j].x < chars[j-1].x) {
				chars[j], chars[j-1] = chars[j-1], chars[j]
			} else {
				break
			}
		}
	}

	var runs []run
	i := 0
	for i < len(chars) {
		// Start a new run
		r := run{x: chars[i].x, y: chars[i].y, join: chars[i].join}
		focused := chars[i].focused
		var sb strings.Builder
		if focused {
			sb.WriteString(focusedStr)
		} else {
			sb.WriteString(unfocusedStr)
		}
		sb.WriteRune(chars[i].ch)
		j := i + 1
		for j < len(chars) && chars[j].y == r.y && chars[j].x == chars[j-1].x+1 &&
			chars[j].focused == focused && chars[j].join == r.join {
			sb.WriteRune(chars[j].ch)
			j++
		}
		sb.WriteString(reset)
		r.text = sb.String()
		runs = append(runs, r)
		i = j
	}

	// Create one layer per run. The cell where a divider meets a chrome rule is
	// on the dock's or the sidebar's own row, both of which compose above the
	// separators, so it rides above them to land on the rule it joins.
	layers := make([]*lipgloss.Layer, len(runs))
	for idx, r := range runs {
		z := config.ZIndexSeparators
		if r.join {
			z = config.ZIndexDock + 1
		}
		layers[idx] = lipgloss.NewLayer(r.text).
			X(r.x).Y(r.y).
			Z(z).
			ID(fmt.Sprintf("sep-%d-%d", r.y, r.x))
	}

	return layers
}

// sgrForeground renders c as a truecolor SGR foreground sequence.
func sgrForeground(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// borderPerimeter is the one-cell ring around the focused window, clipped to the
// tiled bounds. left/right/top/bottom are the ring's own coordinates, which may
// fall outside bounds when the window touches a screen edge; the clipped fields
// bound how far along each side the ring actually reaches. A zero value (ok
// false) matches nothing, so an unfocused or absent window costs nothing.
type borderPerimeter struct {
	ok                                       bool
	left, right, top, bottom                 int
	clipLeft, clipRight, clipTop, clipBottom int
	// capLeft/capRight are false on a side where the ring falls on the sidebar's
	// own edge rule. Capping there draws the window's corner on the rule the
	// sidebar already drew, and the two read as a doubled border.
	capLeft, capRight bool
}

// contains reports whether the cell lies on the focused window's perimeter.
// Both the sides and the corners of the ring count.
func (p borderPerimeter) contains(x, y int) bool {
	if !p.ok {
		return false
	}
	onVertical := (x == p.left || x == p.right) && y >= p.clipTop && y <= p.clipBottom
	onHorizontal := (y == p.top || y == p.bottom) && x >= p.clipLeft && x <= p.clipRight
	return onVertical || onHorizontal
}

// corner returns the border glyph for a corner of the focused perimeter, bending
// into the focused window. It is only reported where the ring actually turns,
// which for a window running to the edge of the content region is the cell on
// the chrome's rule: pulling the cap inside the region instead would end the
// divider on a glyph that paints half a cell, a cell short of the boundary it
// divides up to.
func (p borderPerimeter) corner(x, y int, border lipgloss.Border) (rune, bool) {
	if !p.ok {
		return 0, false
	}
	atLeft := p.capLeft && x == p.left
	atRight := p.capRight && x == p.right
	atTop := y == p.top
	atBottom := y == p.bottom

	switch {
	case atTop && atLeft:
		return firstRune(border.TopLeft, '╭'), true
	case atTop && atRight:
		return firstRune(border.TopRight, '╮'), true
	case atBottom && atLeft:
		return firstRune(border.BottomLeft, '╰'), true
	case atBottom && atRight:
		return firstRune(border.BottomRight, '╯'), true
	}
	return 0, false
}

// focusPerimeter returns the perimeter ring of the focused tiled window. It
// reports ok false when nothing tiled is focused, in which case every separator
// keeps the unfocused styling.
func (m *OS) focusPerimeter(bounds layout.Rect) borderPerimeter {
	win := m.GetFocusedWindow()
	if win == nil || !win.Tiled || win.Minimized || win.IsFloating || win.Width <= 0 || win.Height <= 0 {
		return borderPerimeter{}
	}
	return borderPerimeter{
		ok:     true,
		left:   win.X - 1,
		right:  win.X + win.Width,
		top:    win.Y - 1,
		bottom: win.Y + win.Height,

		clipLeft:   max(win.X-1, bounds.X),
		clipRight:  min(win.X+win.Width, bounds.X+bounds.W-1),
		clipTop:    max(win.Y-1, bounds.Y),
		clipBottom: min(win.Y+win.Height, bounds.Y+bounds.H-1),

		capLeft:  m.GetLeftMargin() == 0 || win.X-1 >= bounds.X,
		capRight: m.GetRightMargin() == 0 || win.X+win.Width < bounds.X+bounds.W,
	}
}

// firstRune returns the first rune from s, or fallback if s is empty.
func firstRune(s string, fallback rune) rune {
	for _, r := range s {
		return r
	}
	return fallback
}
