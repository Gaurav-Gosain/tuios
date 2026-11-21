package app

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) separatorSplits() []layout.SplitLine {
	if !m.panesBorderless() {
		return nil
	}
	if m.UseScrollingLayout {
		bounds := m.GetBSPBounds()
		var splits []layout.SplitLine
		for _, s := range layout.SplitsBetween(m.tiledPaneRects(), m.separatorGap()) {
			if clipped, ok := clipSplit(s, bounds); ok {
				splits = append(splits, clipped)
			}
		}
		return splits
	}
	if !m.UseBSPLayout {
		return m.clipScratchSplits(layout.SplitsBetween(m.tiledPaneRects(), m.separatorGap()))
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return nil
	}
	bounds, c := m.GetBSPBounds(), m.zoomCanvasNow
	if c.on {
		bounds = c.bounds
	}
	splits := tree.CollectSplits(bounds, m.separatorGap())
	for i := range splits {
		splits[i] = c.applySplit(splits[i])
	}
	return m.clipScratchSplits(splits)
}

func (m *OS) clipScratchSplits(splits []layout.SplitLine) []layout.SplitLine {
	if !m.zoomCanvasNow.on || !m.InScratchView() {
		return splits
	}
	bounds := m.GetBSPBounds()
	out := splits[:0]
	for _, s := range splits {
		if clipped, ok := clipSplit(s, bounds); ok {
			out = append(out, clipped)
		}
	}
	return out
}

type paneLayer struct {
	layout.Rect
	moving bool
}

func (m *OS) tiledPaneLayers() []paneLayer {
	inFlight := make(map[*terminal.Window]struct{}, len(m.Animations))
	for _, a := range m.Animations {
		if a.Complete || a.Window == nil {
			continue
		}
		w := a.Window
		if w.X == a.EndX && w.Y == a.EndY && w.Width == a.EndWidth && w.Height == a.EndHeight {
			continue
		}
		inFlight[w] = struct{}{}
	}
	wins := make([]*terminal.Window, 0, len(m.Windows))
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating || !w.Tiled {
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

func (m *OS) tiledPaneRects() []layout.Rect {
	layers := m.tiledPaneLayers()
	rects := make([]layout.Rect, len(layers))
	for i, l := range layers {
		rects[i] = l.Rect
	}
	return rects
}

type dividerLine struct {
	layout.SplitLine
	depth  int
	corner bool
}

const settledDepth = math.MaxInt

func (m *OS) transitioning() bool {
	for _, a := range m.Animations {
		if a.Complete || a.Window == nil {
			continue
		}
		if a.Window.X == a.EndX && a.Window.Y == a.EndY &&
			a.Window.Width == a.EndWidth && a.Window.Height == a.EndHeight {
			continue
		}
		if a.Window.Tiled && a.Window.Workspace == m.CurrentWorkspace && !a.Window.Minimized {
			return true
		}
	}
	return false
}

func (m *OS) dividerLines(bounds layout.Rect) ([]dividerLine, []paneLayer) {
	if !m.transitioning() {
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

func paneEdges(r layout.Rect) [4]layout.SplitLine {
	return [4]layout.SplitLine{
		{Vertical: true, Pos: r.X - 1, From: r.Y, To: r.Y + r.H - 1},
		{Vertical: true, Pos: r.X + r.W, From: r.Y, To: r.Y + r.H - 1},
		{Vertical: false, Pos: r.Y - 1, From: r.X, To: r.X + r.W - 1},
		{Vertical: false, Pos: r.Y + r.H, From: r.X, To: r.X + r.W - 1},
	}
}

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

func clipSplit(s layout.SplitLine, bounds layout.Rect) (layout.SplitLine, bool) {
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

type joinSide uint8

const (
	joinNone joinSide = iota
	joinTop
	joinBottom
	joinLeft
	joinRight
)

type chromeRules struct{ top, bottom, left, right int }

func (m *OS) chromeRules(bounds layout.Rect) chromeRules {
	r := chromeRules{top: -1, bottom: -1, left: -1, right: -1}
	if !m.Settings.BorderJoinsChromeRules() {
		return r
	}
	switch {
	case m.Settings.DockbarPosition == "hidden" || m.Settings.DockCompact:
	case m.Settings.DockbarPosition == "top":
		r.top = bounds.Y - 1
	default:
		r.bottom = bounds.Y + bounds.H
	}
	if m.PaneLeft() > 0 {
		r.left = bounds.X - 1
	}
	if m.GetRightMargin() > 0 {
		r.right = bounds.X + bounds.W
	}
	return r
}

type cell struct {
	vert, horiz bool
	junction    bool
	join        joinSide
}

func (c *cell) isVert() bool  { return c != nil && c.vert }
func (c *cell) isHoriz() bool { return c != nil && c.horiz }

func (m *OS) renderSeparatorOverlay() []*lipgloss.Layer {
	if m.zoomCoversRegion(m.zoomedWindow()) {
		return nil
	}

	bounds := m.GetBSPBounds()
	splits, stack := m.dividerLines(bounds)
	if len(splits) == 0 {
		return nil
	}

	viewW := m.GetRenderWidth()
	viewH := m.GetRenderHeight()

	rules := m.chromeRules(bounds)
	border := m.Settings.GetBorderForStyle()
	focus := m.focusPerimeter(bounds)
	unfocusedStr := sgrForeground(m.unfocusedBorderColor())
	state := m.focusedStateBorder(m.GetFocusedWindow())
	focusColor := m.focusedBorderColor(state)
	if state == nil && focus.ok && m.gradientOn() {
		m.markGradient("", image.Rect(focus.left, focus.top, focus.right+1, focus.bottom+1), focusColor)
	}
	focusedStr := "\x1b[1m" + sgrForeground(focusColor)

	key := separatorKey{
		bounds: bounds, viewW: viewW, viewH: viewH,
		rules: rules, border: border, focus: focus,
		unfocused: unfocusedStr, focused: focusedStr,
	}
	if memo := &m.separatorMemo; memo.valid && memo.key == key &&
		slices.Equal(memo.splits, splits) && slices.Equal(memo.stack, stack) {
		return memo.layers
	}
	layers := m.buildSeparatorLayers(splits, stack, key)
	m.separatorMemo = separatorMemo{valid: true, key: key, splits: splits, stack: stack, layers: layers}
	return layers
}

type separatorKey struct {
	bounds             layout.Rect
	viewW, viewH       int
	rules              chromeRules
	border             lipgloss.Border
	focus              borderPerimeter
	unfocused, focused string
}

type separatorMemo struct {
	valid  bool
	key    separatorKey
	splits []dividerLine
	stack  []paneLayer
	layers []*lipgloss.Layer
}

func (m *OS) buildSeparatorLayers(splits []dividerLine, stack []paneLayer, key separatorKey) []*lipgloss.Layer {
	bounds, viewW, viewH := key.bounds, key.viewW, key.viewH
	rules, border, focus := key.rules, key.border, key.focus

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

	g := dividerGlyphs(border)
	chVert, chHoriz := g.vert, g.horiz
	chCross, chTRight, chTLeft := g.cross, g.tRight, g.tLeft
	chTDown, chTUp := g.tDown, g.tUp

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

	unfocusedStr, focusedStr := key.unfocused, key.focused
	reset := "\x1b[0m"

	type run struct {
		x, y int
		text string
		join bool
	}

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

type dividerGlyphSet struct {
	vert, horiz                      rune
	cross, tRight, tLeft, tDown, tUp rune
	topLeft, topRight                rune
	bottomLeft, bottomRight          rune
}

func dividerGlyphs(border lipgloss.Border) dividerGlyphSet {
	vert := firstRune(border.Left, '│')
	horiz := firstRune(border.Top, '─')
	return dividerGlyphSet{
		vert:        vert,
		horiz:       horiz,
		cross:       firstRune(border.Middle, vert),
		tRight:      firstRune(border.MiddleLeft, vert),
		tLeft:       firstRune(border.MiddleRight, vert),
		tDown:       firstRune(border.MiddleTop, horiz),
		tUp:         firstRune(border.MiddleBottom, horiz),
		topLeft:     firstRune(border.TopLeft, vert),
		topRight:    firstRune(border.TopRight, vert),
		bottomLeft:  firstRune(border.BottomLeft, vert),
		bottomRight: firstRune(border.BottomRight, vert),
	}
}

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

func sgrForeground(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

type borderPerimeter struct {
	ok                                       bool
	left, right, top, bottom                 int
	clipLeft, clipRight, clipTop, clipBottom int
	capLeft, capRight                        bool
}

func (p borderPerimeter) contains(x, y int) bool {
	if !p.ok {
		return false
	}
	onVertical := (x == p.left || x == p.right) && y >= p.clipTop && y <= p.clipBottom
	onHorizontal := (y == p.top || y == p.bottom) && x >= p.clipLeft && x <= p.clipRight
	return onVertical || onHorizontal
}

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

		capLeft:  m.PaneLeft() == 0 || win.X-1 >= bounds.X,
		capRight: m.GetRightMargin() == 0 || win.X+win.Width < bounds.X+bounds.W,
	}
}

func firstRune(s string, fallback rune) rune {
	for _, r := range s {
		return r
	}
	return fallback
}
