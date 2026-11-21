package app

import (
	"image"
	"slices"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type frameCanvas struct {
	uv.Buffer
	renderer  frameRenderer
	blank     uv.Line
	ground    uv.Line
	groundKey string
}

func (c *frameCanvas) WidthMethod() uv.WidthMethod {
	return ansi.GraphemeWidth
}

func (c *frameCanvas) Render() string {
	return c.renderer.render(c.Lines)
}

type cellLayer struct {
	content              string
	w, h                 int
	buf                  uv.Buffer
	spill                []bool
	blank                uv.Line
	gen                  uint64
	fillRect             image.Rectangle
	fillInner, fillOuter string
}

const wideMargin = 8

func (cl *cellLayer) WidthMethod() uv.WidthMethod {
	return ansi.GraphemeWidth
}

func (cl *cellLayer) update(content string, w, h int, fill *layerFill) {
	if cl.same(content, fill) {
		return
	}
	cl.content = content
	cl.w, cl.h = w, h
	cl.fillRect, cl.fillInner, cl.fillOuter = fill.rect, fill.inner.key, fill.outer.key
	cl.buf.Resize(cl.w+wideMargin, cl.h)
	cl.blank = clearLines(cl.buf.Lines, cl.blank)
	uv.NewStyledString(content).Draw(&cellLayerScreen{cl}, uv.Rect(0, 0, cl.w, cl.h))
	if fill.on() {
		paintLayerFill(cl.buf.Lines, cl.w, cl.h, fill)
	}
	cl.spill = slices.Grow(cl.spill[:0], cl.h)[:cl.h]
	for row, line := range cl.buf.Lines {
		cl.spill[row] = false
		for col := max(cl.w-wideMargin, 0); col < cl.w; col++ {
			if c := &line[col]; c.Width > 1 && col+c.Width > cl.w {
				cl.spill[row] = true
				break
			}
		}
	}
}

func (cl *cellLayer) same(content string, fill *layerFill) bool {
	return cl.content == content && cl.w >= 0 && cl.fillRect == fill.rect &&
		cl.fillInner == fill.inner.key && cl.fillOuter == fill.outer.key
}

func clearLines(lines []uv.Line, blank uv.Line) uv.Line {
	w := 0
	for _, l := range lines {
		w = max(w, len(l))
	}
	if len(blank) < w {
		blank = make(uv.Line, w)
		for i := range blank {
			blank[i] = uv.EmptyCell
		}
	}
	for _, l := range lines {
		copy(l, blank[:len(l)])
	}
	return blank
}

func (c *frameCanvas) Clear() {
	c.blank = clearLines(c.Lines, c.blank)
}

func (c *frameCanvas) ClearTo(g ground) {
	if !g.on() {
		c.Clear()
		return
	}
	w := 0
	for _, l := range c.Lines {
		w = max(w, len(l))
	}
	if c.groundKey != g.key || len(c.ground) < w {
		c.ground = slices.Grow(c.ground[:0], w)[:w]
		blank := desktopBlank(g)
		for i := range c.ground {
			c.ground[i] = blank
		}
		c.groundKey = g.key
	}
	for _, l := range c.Lines {
		copy(l, c.ground[:len(l)])
	}
}

type cellLayerScreen struct {
	*cellLayer
}

func (s *cellLayerScreen) Bounds() uv.Rectangle         { return s.buf.Bounds() }
func (s *cellLayerScreen) CellAt(x, y int) *uv.Cell     { return s.buf.CellAt(x, y) }
func (s *cellLayerScreen) SetCell(x, y int, c *uv.Cell) { s.buf.SetCell(x, y, c) }
func (s *cellLayerScreen) WidthMethod() uv.WidthMethod  { return ansi.GraphemeWidth }

func (cl *cellLayer) blit(canvas *frameCanvas, x, y int) {
	for row := range cl.h {
		cy := y + row
		if cy < 0 || cy >= len(canvas.Lines) {
			continue
		}
		line := canvas.Lines[cy]
		src := cl.buf.Lines[row]
		if x >= 0 && x+cl.w <= len(line) && !cl.spill[row] &&
			!isPlaceholder(&line[x]) && (x+cl.w == len(line) || !isPlaceholder(&line[x+cl.w])) {
			copy(line[x:x+cl.w], src[:cl.w])
			continue
		}
		for col := range cl.w {
			line.Set(x+col, nil)
		}
		for col := range cl.w {
			c := &src[col]
			if c.IsZero() {
				continue
			}
			line.Set(x+col, c)
		}
	}
}

func isPlaceholder(c *uv.Cell) bool {
	return c.Width == 0
}

type composedLayer struct {
	layer  *lipgloss.Layer
	bounds image.Rectangle
	cells  *cellLayer
	pane   bool
}

func (m *OS) composeLayersIn(canvas *frameCanvas, layers []*lipgloss.Layer, paneLayers int) {
	m.composeLayers(canvas, layers, paneLayers, nil)
}

type keptCanvas struct {
	valid  bool
	ground string
	w, h   int
	layers []keptLayer
}

type keptLayer struct {
	cells  *cellLayer
	bounds image.Rectangle
	z      int
}

type keepRequest struct {
	ground   ground
	keepable bool
	pane     *directPane
}

func (k *keptCanvas) matches(canvas *frameCanvas, g ground, ordered []composedLayer) bool {
	if !k.valid || k.w != canvas.Width() || k.h != canvas.Height() || k.ground != g.key || len(k.layers) != len(ordered) {
		return false
	}
	for i := range ordered {
		e, was := &ordered[i], &k.layers[i]
		if e.cells != was.cells || e.bounds != was.bounds || layerZ(e.layer) != was.z {
			return false
		}
	}
	return true
}

func (k *keptCanvas) record(canvas *frameCanvas, g ground, ordered []composedLayer) {
	k.valid = true
	k.w, k.h, k.ground = canvas.Width(), canvas.Height(), g.key
	k.layers = k.layers[:0]
	for i := range ordered {
		e := &ordered[i]
		k.layers = append(k.layers, keptLayer{cells: e.cells, bounds: e.bounds, z: layerZ(e.layer)})
	}
}

func (m *OS) composeLayers(canvas *frameCanvas, layers []*lipgloss.Layer, paneLayers int, keep *keepRequest) bool {
	m.composeGen++
	if m.layerCells == nil {
		m.layerCells = make(map[string]*cellLayer)
	}

	ordered := m.composeScratch[:0]
	ordered = append(ordered, composedLayer{bounds: image.Rect(0, 0, 0, 1)})
	now := time.Now()
	view := m.sessionView
	slide, sliding := m.slideAt(now)
	if sliding {
		if !view.on {
			view = sessionView{on: true, clip: slide.area}
		}
		view.dx += slide.newShift
		view.clip = view.clip.Intersect(slide.newClip)
	}
	slideDrawn := !sliding
	for i, l := range layers {
		if l == nil {
			continue
		}
		entry := composedLayer{layer: l, pane: view.on && i < paneLayers}
		if id := l.GetID(); id != "" {
			cl := m.layerCells[id]
			if cl == nil {
				cl = &cellLayer{w: -1}
				m.layerCells[id] = cl
			}
			cl.gen = m.composeGen
			entry.cells = cl
		}
		entry.bounds = image.Rect(l.GetX(), l.GetY(), l.GetX()+l.Width(), l.GetY()+l.Height())
		ordered = append(ordered, entry)
	}
	slices.SortStableFunc(ordered, func(a, b composedLayer) int {
		return layerZ(a.layer) - layerZ(b.layer)
	})

	grounds := m.frameGrounds()
	painted := grounds.any()

	popup, popupDim := m.popupScrimTarget()
	scrimmed := m.Settings.ModalDim <= 0 && popupDim <= 0
	dialogScrim := popup != "" && m.isTMLPopup(m.GetFocusedWindow())
	chromeScrim := false
	fading := m.motion.fading > 0
	shimmer := len(m.motion.rail) > 0

	var pane *directPane
	var paneID string
	var paneRect image.Rectangle
	if keep != nil && keep.pane != nil && keep.pane.w != nil {
		pane, paneID, paneRect = keep.pane, keep.pane.w.ID, paneContentRect(keep.pane.w)
	}

	quiet := keep != nil && keep.keepable && !sliding && !view.on && popup == "" && !fading && !shimmer &&
		!m.gradient.on && m.hints == nil && m.paneLabels == nil && !m.dropZoneShown && !m.dropRingOn()
	if quiet && m.Settings.ModalDim > 0 {
		for i := range ordered {
			if l := ordered[i].layer; l != nil && scrimBehind(l.GetID()) {
				quiet = false
				break
			}
		}
	}
	plain := quiet
	drawn, reparsed := ordered[:0:0], false
	if plain {
		drawn = ordered[1:]
		abovePane := false
		for i := range drawn {
			e := &drawn[i]
			if e.layer == nil || e.cells == nil || (abovePane && e.bounds.Overlaps(paneRect)) {
				plain = false
				break
			}
			if e.layer.GetID() == paneID {
				abovePane = true
			}
			var fill layerFill
			if painted {
				fill = m.layerFill(e.layer.GetID(), e.bounds, e.layer.Width(), e.layer.Height(), &grounds)
			}
			if !e.cells.same(e.layer.GetContent(), &fill) {
				e.cells.update(e.layer.GetContent(), e.layer.Width(), e.layer.Height(), &fill)
				reparsed = true
			}
		}
	}
	var ground ground
	if keep != nil {
		ground = keep.ground
	}
	kept := plain && !reparsed && m.kept.matches(canvas, ground, drawn)
	if !kept && keep != nil {
		if pane != nil && pane.held {
			m.saveHeldPane(canvas, paneRect)
		}
		canvas.ClearTo(keep.ground)
	}

	area := canvas.Bounds()
	paneDrawn := false
	drawPane := func(id string) {
		if pane != nil && !paneDrawn && id == paneID {
			m.copyDirectPane(canvas, pane, kept)
			paneDrawn = true
		}
	}
	for _, cl := range ordered {
		if kept {
			break
		}
		if cl.layer == nil {
			continue
		}
		if cl.pane {
			at := cl.bounds.Add(image.Pt(view.dx, view.dy))
			if !at.Overlaps(view.clip) {
				continue
			}
			if !scrimmed && (popup != "" && cl.layer.GetID() == popup) {
				m.drawImageSymbols(canvas)
				m.applyScrim(canvas, popupDim)
				scrimmed, chromeScrim = true, true
			}
			m.drawPaneLayer(canvas, cl, at, view.clip, painted, &grounds)
			drawPane(cl.layer.GetID())
			if chromeScrim && dialogScrim && cl.layer.GetID() == popup {
				m.scrimDialogGround(canvas, at.Intersect(view.clip), popupDim)
			}
			if m.gradient.on && m.gradientLayer(cl.layer.GetID()) {
				m.applyBorderGradient(canvas, at.Intersect(view.clip), image.Pt(view.dx, view.dy))
			}
			if m.hints != nil {
				m.applyHints(canvas, cl.layer.GetID(), &grounds)
			}
			if m.paneLabels != nil {
				m.applyPaneLabels(canvas, cl.layer.GetID())
			}
			if m.dropZoneShown {
				m.applyDropZone(canvas, cl.layer.GetID())
			}
			if m.dropRingOn() {
				m.applyDropRing(canvas, cl.layer.GetID())
			}
			continue
		}
		if !cl.bounds.Overlaps(area) {
			continue
		}
		if !slideDrawn && cl.bounds.Overlaps(slide.area) {
			m.drawSlideOld(canvas, slide)
			slideDrawn = true
		}
		if !scrimmed && (popup != "" && cl.layer.GetID() == popup) {
			m.drawImageSymbols(canvas)
			m.applyScrim(canvas, popupDim)
			scrimmed, chromeScrim = true, true
		}
		if !scrimmed && m.Settings.ModalDim > 0 && scrimBehind(cl.layer.GetID()) {
			m.drawImageSymbols(canvas)
			m.applyScrim(canvas, m.Settings.ModalDim)
			scrimmed = true
		}
		m.drawComposedLayer(canvas, cl, painted, &grounds, area)
		drawPane(cl.layer.GetID())
		if chromeScrim && scrimmedChrome(cl.layer.GetID()) {
			m.applyScrimIn(canvas, cl.bounds, popupDim)
		}
		if chromeScrim && dialogScrim && cl.layer.GetID() == popup {
			m.scrimDialogGround(canvas, cl.bounds, popupDim)
		}
		if m.gradient.on && m.gradientLayer(cl.layer.GetID()) {
			m.applyBorderGradient(canvas, cl.bounds, image.Point{})
		}
		if m.hints != nil {
			m.applyHints(canvas, cl.layer.GetID(), &grounds)
		}
		if m.paneLabels != nil {
			m.applyPaneLabels(canvas, cl.layer.GetID())
		}
		if m.dropZoneShown {
			m.applyDropZone(canvas, cl.layer.GetID())
		}
		if m.dropRingOn() {
			m.applyDropRing(canvas, cl.layer.GetID())
		}
		switch id := cl.layer.GetID(); {
		case fading:
			m.applyFade(canvas, id, cl.bounds, now)
			if shimmer && id == sidebarLayerID {
				m.applyShimmer(canvas, now)
			}
		case shimmer && id == sidebarLayerID:
			m.applyShimmer(canvas, now)
		}
	}

	if !slideDrawn {
		m.drawSlideOld(canvas, slide)
	}
	if pane != nil && !paneDrawn {
		m.copyDirectPane(canvas, pane, kept)
	}
	if quiet {
		m.checkFrame(canvas, ordered, painted, &grounds, ground, pane, kept)
	}
	if pane != nil {
		m.directHold.pure = plain
		m.directHold.saved = nil
	}

	if !m.keepLayerCells {
		for id, cl := range m.layerCells {
			if cl.gen != m.composeGen {
				delete(m.layerCells, id)
			}
		}
	}
	switch {
	case kept:
	case plain:
		m.kept.record(canvas, ground, drawn)
	default:
		m.kept.valid = false
	}
	clear(ordered)
	m.composeScratch = ordered[:0]
	return kept
}

func (m *OS) drawComposedLayer(canvas *frameCanvas, cl composedLayer, painted bool, grounds *frameGrounds, area image.Rectangle) {
	if cl.cells != nil {
		var fill layerFill
		if painted {
			fill = m.layerFill(cl.layer.GetID(), cl.bounds, cl.layer.Width(), cl.layer.Height(), grounds)
		}
		cl.cells.update(cl.layer.GetContent(), cl.layer.Width(), cl.layer.Height(), &fill)
		cl.cells.blit(canvas, cl.bounds.Min.X, cl.bounds.Min.Y)
		return
	}
	uv.NewStyledString(cl.layer.GetContent()).Draw(canvas, cl.bounds)
	if g := grounds[surfaceDesktop]; g.on() {
		paintGround(canvas.Lines, cl.bounds.Intersect(area), g)
	}
}

func (m *OS) drawPaneLayer(canvas *frameCanvas, cl composedLayer, at, clip image.Rectangle, painted bool, grounds *frameGrounds) {
	if cl.cells == nil {
		uv.NewStyledString(cl.layer.GetContent()).Draw(&clippedScreen{canvas, clip}, at)
		return
	}
	var fill layerFill
	if painted {
		fill = m.layerFill(cl.layer.GetID(), cl.bounds, cl.layer.Width(), cl.layer.Height(), grounds)
	}
	cl.cells.update(cl.layer.GetContent(), cl.layer.Width(), cl.layer.Height(), &fill)
	if at.In(clip) {
		cl.cells.blit(canvas, at.Min.X, at.Min.Y)
		return
	}
	cl.cells.blitClipped(canvas, at.Min.X, at.Min.Y, clip)
}

func (cl *cellLayer) blitClipped(canvas *frameCanvas, x, y int, clip image.Rectangle) {
	for row := range cl.h {
		cy := y + row
		if cy < clip.Min.Y || cy >= clip.Max.Y || cy < 0 || cy >= len(canvas.Lines) {
			continue
		}
		line := canvas.Lines[cy]
		src := cl.buf.Lines[row]
		from, to := max(x, clip.Min.X), min(x+cl.w, clip.Max.X)
		if from >= to {
			continue
		}
		if from >= 0 && to <= len(line) && !cl.spill[row] &&
			!isPlaceholder(&line[from]) && (to == len(line) || !isPlaceholder(&line[to])) &&
			!isPlaceholder(&src[from-x]) && !isPlaceholder(&src[to-x]) {
			copy(line[from:to], src[from-x:to-x])
			continue
		}
		for cx := from; cx < to; cx++ {
			line.Set(cx, nil)
		}
		for cx := from; cx < to; cx++ {
			c := &src[cx-x]
			if c.IsZero() {
				continue
			}
			line.Set(cx, c)
		}
	}
}

type clippedScreen struct {
	*frameCanvas
	clip image.Rectangle
}

func (s *clippedScreen) SetCell(x, y int, c *uv.Cell) {
	if image.Pt(x, y).In(s.clip) {
		s.frameCanvas.SetCell(x, y, c)
	}
}

func layerZ(l *lipgloss.Layer) int {
	if l == nil {
		return 0
	}
	return l.GetZ()
}
