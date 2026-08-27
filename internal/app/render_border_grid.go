package app

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// separatorSplits returns the divider lines for the layout that placed the
// panes. The two tilers reserve their gaps differently, so each has to answer
// for its own: the BSP tree knows its splits, and the master-stack tiler has
// them read back off the panes it positioned. Asking the tree while master-stack
// is driving is what used to paint a line down the pane beside it, because the
// tree survives the mode switch holding the geometry it last laid out.
func (m *OS) separatorSplits() []layout.SplitLine {
	// A divider stands in for the borders two panes gave up. Panes still drawing
	// their own have nothing to share, so nothing is drawn between them whatever
	// gap the user asked for: appearance.gap is empty ground, not a rule down
	// the middle of it.
	//
	// The frame is already right without this, because View only asks for the
	// overlay when the panes are borderless. It is here so the answer does not
	// depend on the caller remembering: the tree knows the gap and not what the
	// panes look like, and it will hand out a divider for a pane that has its
	// own border to anyone who asks.
	if !m.panesBorderless() {
		return nil
	}
	if m.UseScrollingLayout {
		return nil
	}
	if !m.UseBSPLayout {
		return layout.SplitsBetween(m.tiledPaneRects(), m.separatorGap())
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return nil
	}
	return tree.CollectSplits(m.GetBSPBounds(), m.separatorGap())
}

// paneLayer is a tiled pane as the divider grid sees it: the rectangle it holds
// on this frame, and whether it is in flight toward another one.
type paneLayer struct {
	layout.Rect
	moving bool
}

// tiledPaneLayers returns the panes currently tiled on screen, ordered bottom to
// top the way the compositor stacks them.
func (m *OS) tiledPaneLayers() []paneLayer {
	inFlight := make(map[*terminal.Window]struct{}, len(m.Animations))
	for _, a := range m.Animations {
		if !a.Complete && a.Window != nil {
			inFlight[a.Window] = struct{}{}
		}
	}
	wins := make([]*terminal.Window, 0, len(m.Windows))
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.Minimizing || w.IsFloating || !w.Tiled {
			continue
		}
		wins = append(wins, w)
	}
	sort.SliceStable(wins, func(a, b int) bool { return wins[a].Z < wins[b].Z })
	layers := make([]paneLayer, len(wins))
	for i, w := range wins {
		_, moving := inFlight[w]
		layers[i] = paneLayer{Rect: layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}, moving: moving}
	}
	return layers
}

// tiledPaneRects returns the rectangles of the panes currently tiled on screen.
func (m *OS) tiledPaneRects() []layout.Rect {
	layers := m.tiledPaneLayers()
	rects := make([]layout.Rect, len(layers))
	for i, l := range layers {
		rects[i] = l.Rect
	}
	return rects
}

// dividerLine is a divider the frame will draw, carrying the depth of the pane
// whose edge it is. Panes overlap while a transition is in flight, and a divider
// belonging to the lower one gives way to the pane on top exactly as that pane's
// own content does.
type dividerLine struct {
	layout.SplitLine
	depth int
	// corner marks the single cell diagonally off a pane's rectangle, where two
	// of its edges turn. It runs in neither direction of its own: which arms it
	// grows is decided by the lines that reach it, which is what lets the same
	// cell be a pane's own corner in open space and the head of a division where
	// a third pane meets it.
	corner bool
}

// settledDepth marks a divider that belongs to the layout rather than to one
// pane, so nothing occludes it. The settled layout reserves every divider cell,
// which is why no pane can be covering one.
const settledDepth = math.MaxInt

// transitioning reports whether a tiled pane on screen is mid-animation, so the
// settled layout describes where the panes are going rather than where they are.
func (m *OS) transitioning() bool {
	for _, a := range m.Animations {
		if a.Complete || a.Window == nil {
			continue
		}
		if a.Window.Tiled && a.Window.Workspace == m.CurrentWorkspace && !a.Window.Minimized {
			return true
		}
	}
	return false
}

// dividerLines returns the dividers to draw on this frame.
//
// A settled layout has its divisions in the gaps it reserved for them, and the
// tiler that placed the panes answers for those. A layout in motion has not
// reserved anything yet: its panes are between two layouts, so a division taken
// from either one sits where no pane edge is, and the frame reads as panes
// sliding under a skeleton that is not theirs.
//
// So while panes are moving the divisions are the panes' own edges, which makes
// every one of them a real boundary on the frame being drawn. A pane that has
// not moved contributes the same edges the settled grid would have drawn there,
// and a pane in flight carries its edges with it, so the layout moves as one
// object and the moving pane reads as a pane rather than as a bare rectangle.
func (m *OS) dividerLines(bounds layout.Rect) ([]dividerLine, []paneLayer) {
	// Both tilers answer for their own settled layout. The BSP tree does it
	// because a stacked node divides by raising a title bar rather than by a
	// divider, which is a division the panes' edges cannot tell apart from any
	// other; the master-stack tiler does it because a division is one line on the
	// gap's leading column whatever the gap is, and its panes' own edges are two
	// lines with ground between them.
	//
	// Restricting this to BSP left the two modes disagreeing about one setting:
	// with shared borders on and appearance.gap at 2, BSP drew one divider and a
	// column of ground while master-stack drew a two-column rule. It also left
	// layout.SplitsBetween - written for exactly this - reachable only from a
	// test.
	if !m.UseScrollingLayout && !m.transitioning() {
		splits := m.separatorSplits()
		lines := make([]dividerLine, len(splits))
		for i, s := range splits {
			lines[i] = dividerLine{SplitLine: s, depth: settledDepth}
		}
		return lines, nil
	}
	stack := m.tiledPaneLayers()
	lines := make([]dividerLine, 0, 8*len(stack))
	for depth, r := range stack {
		for _, s := range paneEdges(r.Rect) {
			if clipped, ok := clipSplit(s, bounds); ok {
				lines = append(lines, dividerLine{SplitLine: clipped, depth: depth})
			}
		}
		for _, c := range paneCorners(r.Rect) {
			if _, ok := clipSplit(c, bounds); ok {
				lines = append(lines, dividerLine{SplitLine: c, depth: depth, corner: true})
			}
		}
	}
	return lines, stack
}

// paneEdges returns the four lines one cell outside r's sides, each spanning its
// own side only.
//
// A side stops short of the corner it turns on, which paneCorners answers for
// separately. Running it through the corner instead would lay a line along a
// side that has no divider on it, and that arm is what would come out as a
// crossing where a division only meets another pane's edge.
func paneEdges(r layout.Rect) [4]layout.SplitLine {
	return [4]layout.SplitLine{
		{Vertical: true, Pos: r.X - 1, From: r.Y, To: r.Y + r.H - 1},
		{Vertical: true, Pos: r.X + r.W, From: r.Y, To: r.Y + r.H - 1},
		{Vertical: false, Pos: r.Y - 1, From: r.X, To: r.X + r.W - 1},
		{Vertical: false, Pos: r.Y + r.H, From: r.X, To: r.X + r.W - 1},
	}
}

// paneCorners returns the four cells diagonally off r, each as the single cell
// it is. Pos carries the column and From/To the row, so clipSplit answers for
// them the same way it does for an edge.
func paneCorners(r layout.Rect) [4]layout.SplitLine {
	left, right := r.X-1, r.X+r.W
	top, bottom := r.Y-1, r.Y+r.H
	return [4]layout.SplitLine{
		{Vertical: true, Pos: left, From: top, To: top},
		{Vertical: true, Pos: right, From: top, To: top},
		{Vertical: true, Pos: left, From: bottom, To: bottom},
		{Vertical: true, Pos: right, From: bottom, To: bottom},
	}
}

// clipSplit trims s to the content region, reporting false when nothing of it is
// left. A pane against the region's edge has its outer side on the chrome beyond
// it, which the chrome rules answer for instead.
func clipSplit(s layout.SplitLine, bounds layout.Rect) (layout.SplitLine, bool) {
	// A vertical line's Pos is a column and its extent is rows; a horizontal
	// line's is the other way round.
	posLo, posHi := bounds.X, bounds.X+bounds.W-1
	extLo, extHi := bounds.Y, bounds.Y+bounds.H-1
	if !s.Vertical {
		posLo, posHi, extLo, extHi = extLo, extHi, posLo, posHi
	}
	if s.Pos < posLo || s.Pos > posHi {
		return s, false
	}
	s.From, s.To = max(s.From, extLo), min(s.To, extHi)
	return s, s.From <= s.To
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
	// Only a divider with a stroke has something to meet the rule with. The rest
	// stop at the boundary, since a cell of fill covers the rule rather than
	// joining it and a cell of the hidden style rubs it out.
	if !config.BorderJoinsChromeRules() {
		return r
	}
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

// cell is one cell of the divider grid: which axes run through it, and whether
// it is the cell where a divider meets a chrome rule. A nil receiver is an empty
// cell, so a neighbour probe can ask about a cell that was never filled in.
type cell struct {
	vert, horiz bool
	junction    bool
	join        joinSide
}

func (c *cell) isVert() bool  { return c != nil && c.vert }
func (c *cell) isHoriz() bool { return c != nil && c.horiz }

// renderSeparatorOverlay renders thin separator lines between tiled panes.
// Each separator line is its own lipgloss Layer to avoid occluding content.
func (m *OS) renderSeparatorOverlay() []*lipgloss.Layer {
	// Don't render shared borders when a window is zoomed
	if fw := m.GetFocusedWindow(); fw != nil && fw.Zoomed {
		return nil
	}

	bounds := m.GetBSPBounds()
	splits, stack := m.dividerLines(bounds)
	if len(splits) == 0 {
		return nil
	}

	// Nothing may be painted into a cell a pane's guest owns, and two panes
	// crossing mid-transition put one of them over the other's edge. So an edge
	// gives way to any pane in front of it, and a pane that is standing still
	// gives way to every pane it overlaps: only a pane travelling draws its own
	// box over a neighbour, which is what its border does when it has one. A
	// settled layout reserves every divider cell, so this never fires there.
	occluded := func(x, y, depth int) bool {
		if depth == settledDepth {
			return false
		}
		for d, r := range stack {
			if d == depth || (d < depth && stack[depth].moving) {
				continue
			}
			if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
				return true
			}
		}
		return false
	}

	viewW := m.GetRenderWidth()
	viewH := m.GetRenderHeight()

	// Collect all separator characters with positions
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
		if s.corner {
			if s.Pos >= 0 && s.Pos < viewW && s.From >= 0 && s.From < viewH &&
				!occluded(s.Pos, s.From, s.depth) {
				get(s.Pos, s.From).junction = true
			}
			continue
		}
		if s.Vertical {
			if s.Pos < 0 || s.Pos >= viewW {
				continue
			}
			for y := max(s.From, 0); y <= min(s.To, viewH-1); y++ {
				if occluded(s.Pos, y, s.depth) {
					continue
				}
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
				if occluded(x, s.Pos, s.depth) {
					continue
				}
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
	g := dividerGlyphs(border)
	chVert, chHoriz := g.vert, g.horiz
	chCross, chTRight, chTLeft := g.cross, g.tRight, g.tLeft
	chTDown, chTUp := g.tDown, g.tUp

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
			// Check horizontal neighbors for T-junctions. An arm is only grown
			// toward a neighbour carrying a line that runs into this one: two
			// dividers a cell apart on the same axis are two edges that have not
			// closed up yet, which a transition leaves on the frame, and reading
			// one as an arm of the other would draw a tee where nothing meets.
			hasL := grid[[2]int{x - 1, y}].isHoriz()
			hasR := grid[[2]int{x + 1, y}].isHoriz()
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
			hasU := grid[[2]int{x, y - 1}].isVert()
			hasD := grid[[2]int{x, y + 1}].isVert()
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
		case c.junction:
			r, ok := junctionGlyph(
				grid[[2]int{x - 1, y}].isHoriz(), grid[[2]int{x + 1, y}].isHoriz(),
				grid[[2]int{x, y - 1}].isVert(), grid[[2]int{x, y + 1}].isVert(),
				g,
			)
			if !ok {
				continue
			}
			ch = r
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

// dividerGlyphSet is the glyph a divider cell is drawn with for each shape it
// can take. One set built once, so every cell of the grid answers the question
// the same way whether the layout is settled or in motion.
type dividerGlyphSet struct {
	vert, horiz                      rune
	cross, tRight, tLeft, tDown, tUp rune
	topLeft, topRight                rune
	bottomLeft, bottomRight          rune
}

// dividerGlyphs resolves the set for a border style.
//
// The arms of a junction are what show two strokes meeting. A style that leaves
// them empty is drawn with fills, whose cells already touch along the edge they
// share, so its junction is its own divider glyph carried through: falling back
// to a box-drawing arm welds a line onto a bar of blocks.
func dividerGlyphs(border lipgloss.Border) dividerGlyphSet {
	vert := firstRune(border.Left, '│')
	horiz := firstRune(border.Top, '─')
	return dividerGlyphSet{
		vert:        vert,
		horiz:       horiz,
		cross:       firstRune(border.Middle, vert),
		tRight:      firstRune(border.MiddleLeft, vert),    // ├ T pointing right
		tLeft:       firstRune(border.MiddleRight, vert),   // ┤ T pointing left
		tDown:       firstRune(border.MiddleTop, horiz),    // ┬ T pointing down
		tUp:         firstRune(border.MiddleBottom, horiz), // ┴ T pointing up
		topLeft:     firstRune(border.TopLeft, vert),
		topRight:    firstRune(border.TopRight, vert),
		bottomLeft:  firstRune(border.BottomLeft, vert),
		bottomRight: firstRune(border.BottomRight, vert),
	}
}

// junctionGlyph picks the glyph for a cell that runs in no direction of its own
// from the arms that reach it. A pane alone in open space turns its own corner
// here; where a division meets the cell it becomes the tee or crossing that
// meeting needs. A cell nothing reaches reports false and is left unpainted.
func junctionGlyph(l, r, u, d bool, g dividerGlyphSet) (rune, bool) {
	switch {
	case l && r && u && d:
		return g.cross, true
	case l && r && d:
		return g.tDown, true
	case l && r && u:
		return g.tUp, true
	case u && d && r:
		return g.tRight, true
	case u && d && l:
		return g.tLeft, true
	case r && d:
		return g.topLeft, true
	case l && d:
		return g.topRight, true
	case r && u:
		return g.bottomLeft, true
	case l && u:
		return g.bottomRight, true
	case l || r:
		return g.horiz, true
	case u || d:
		return g.vert, true
	}
	return 0, false
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
