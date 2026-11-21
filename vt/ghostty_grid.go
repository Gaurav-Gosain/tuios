//go:build ghostty

package vt

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"
)

type ghosttyCellDecoder struct {
	ok                bool
	tagLSB, tagMask   uint64
	cpLSB, cpMask     uint64
	styleLSB          uint64
	styleMask         uint64
	wideLSB, wideMask uint64
	linkBit           uint64
	protBit           uint64
	hasProt           bool
	rLSB, gLSB, bLSB  uint64
	palLSB            uint64
}

func newGhosttyCellDecoder() ghosttyCellDecoder {
	var d ghosttyCellDecoder
	var m struct {
		Types map[string]struct {
			Bits map[string]json.RawMessage `json:"bits"`
		} `json:"types"`
	}
	raw := gh.TypeJSON()
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return d
	}
	cell, found := m.Types["GhosttyCell"]
	if !found {
		return d
	}
	type field struct {
		LSB   uint64 `json:"lsb"`
		Width uint64 `json:"width"`
	}
	get := func(name string) (field, bool) {
		rawField, okF := cell.Bits[name]
		if !okF {
			return field{}, false
		}
		var f field
		if err := json.Unmarshal(rawField, &f); err != nil {
			return field{}, false
		}
		return f, f.Width > 0
	}
	tag, ok1 := get("content_tag")
	styleID, ok2 := get("style_id")
	wide, ok3 := get("wide")
	link, ok4 := get("hyperlink")
	var content struct {
		LSB   uint64 `json:"lsb"`
		Width uint64 `json:"width"`
		Arms  map[string]struct {
			Bits map[string]field `json:"bits"`
		} `json:"arms"`
	}
	ok5, ok6 := false, false
	if rawContent, okC := cell.Bits["content"]; okC {
		if err := json.Unmarshal(rawContent, &content); err == nil {
			if arm, okA := content.Arms["CODEPOINT"]; okA {
				if cp, okB := arm.Bits["codepoint"]; okB {
					d.cpLSB = content.LSB + cp.LSB
					d.cpMask = (1 << cp.Width) - 1
					ok5 = true
				}
			}
			rgb := content.Arms["BG_COLOR_RGB"].Bits
			r, okR := rgb["r"]
			g, okG := rgb["g"]
			b, okB := rgb["b"]
			pal, okP := content.Arms["BG_COLOR_PALETTE"].Bits["index"]
			if okR && okG && okB && okP {
				d.rLSB, d.gLSB, d.bLSB = content.LSB+r.LSB, content.LSB+g.LSB, content.LSB+b.LSB
				d.palLSB = content.LSB + pal.LSB
				ok6 = true
			}
		}
	}
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return d
	}
	d.tagLSB, d.tagMask = tag.LSB, (1<<tag.Width)-1
	d.styleLSB, d.styleMask = styleID.LSB, (1<<styleID.Width)-1
	d.wideLSB, d.wideMask = wide.LSB, (1<<wide.Width)-1
	d.linkBit = link.LSB
	if prot, ok := get("protected"); ok {
		d.protBit, d.hasProt = prot.LSB, true
	}
	d.ok = true
	return d
}

type decodedCell struct {
	tag     gh.CellContentTag
	cp      rune
	styleID uint16
	wide    gh.CellWide
	link    bool
	prot    bool
}

func (d *ghosttyCellDecoder) decode(v uint64) decodedCell {
	return decodedCell{
		tag:     gh.CellContentTag((v >> d.tagLSB) & d.tagMask),
		cp:      rune((v >> d.cpLSB) & d.cpMask),
		styleID: uint16((v >> d.styleLSB) & d.styleMask),
		wide:    gh.CellWide((v >> d.wideLSB) & d.wideMask),
		link:    (v>>d.linkBit)&1 != 0,
		prot:    d.hasProt && (v>>d.protBit)&1 != 0,
	}
}

func (d *ghosttyCellDecoder) background(v uint64, tag gh.CellContentTag) gh.StyleColor {
	if tag == gh.CellContentBgColorPalette {
		return gh.StyleColor{Tag: gh.StyleColorPalette, Palette: uint8(v >> d.palLSB)}
	}
	return gh.StyleColor{Tag: gh.StyleColorRGB, RGB: gh.ColorRGB{
		R: uint8(v >> d.rLSB),
		G: uint8(v >> d.gLSB),
		B: uint8(v >> d.bLSB),
	}}
}

type backgroundMemo struct {
	color gh.StyleColor
	style uv.Style
}

func (t *GhosttyTerminal) backgroundOnlyStyle(memo *backgroundMemo, cell *gh.Cell, tag gh.CellContentTag, pal *ghosttyPalette) uv.Style {
	var sc gh.StyleColor
	switch {
	case t.dec.ok:
		sc = t.dec.background(cell.PackedValue(), tag)
	case tag == gh.CellContentBgColorPalette:
		if i, err := cell.ColorPalette(); err == nil {
			sc = gh.StyleColor{Tag: gh.StyleColorPalette, Palette: i}
		}
	default:
		if rgb, err := cell.ColorRGB(); err == nil {
			sc = gh.StyleColor{Tag: gh.StyleColorRGB, RGB: rgb}
		}
	}
	if memo.color != sc {
		memo.color = sc
		memo.style = uv.Style{Bg: t.resolveStyleColor(sc, pal)}
	}
	return memo.style
}

func decodeCellSlow(c *gh.Cell) decodedCell {
	var out decodedCell
	if tag, err := c.ContentTag(); err == nil {
		out.tag = tag
	}
	if cp, err := c.Codepoint(); err == nil {
		out.cp = rune(cp)
	}
	if id, err := c.StyleID(); err == nil {
		out.styleID = id
	}
	if w, err := c.Wide(); err == nil {
		out.wide = w
	}
	if h, err := c.HasHyperlink(); err == nil {
		out.link = h
	}
	if p, err := c.Protected(); err == nil {
		out.prot = p
	}
	return out
}

func (t *GhosttyTerminal) syncLocked() {
	if t.closed.Load() {
		return
	}
	t.flushRestoreLocked()
	if !t.gridStale {
		return
	}
	t.gridStale = false

	activeAlt := t.IsAltScreen()
	idx := 0
	if activeAlt {
		idx = 1
	}
	t.active = idx
	buf := t.bufAt(idx)

	if err := t.rs.Update(t.term); err != nil {
		return
	}
	clear(t.styleCache)
	if err := t.rs.RowIterator(t.ri); err != nil {
		return
	}
	for {
		y, ok := t.ri.NextDirty()
		if !ok {
			break
		}
		if int(y) >= t.height {
			continue
		}
		t.syncRowLocked(buf, int(y))
	}
	_ = t.rs.Clean()

	if x, err := t.term.CursorX(); err == nil {
		t.curX = int(x)
	}
	if y, err := t.term.CursorY(); err == nil {
		t.curY = int(y)
	}
	if vis, err := t.term.CursorVisible(); err == nil {
		t.curHidden = !vis
	}
}

func (t *GhosttyTerminal) syncRowLocked(buf *grid, y int) {
	view, err := t.ri.CellsRaw()
	if err != nil {
		return
	}
	n := view.Len()
	cellsLoaded := false
	memo := kittyPlaceholderMemo{x: -2}
	var bg backgroundMemo
	for x := 0; x < n && x < t.width; x++ {
		cell := view.Cell(x)
		var dc decodedCell
		if t.dec.ok {
			dc = t.dec.decode(cell.PackedValue())
		} else {
			dc = decodeCellSlow(cell)
		}

		switch dc.wide {
		case gh.CellWideSpacerTail:
			continue
		case gh.CellWideSpacerHead:
			buf.SetCell(x, y, &uv.Cell{Content: " ", Width: 1})
			continue
		}

		out := &uv.Cell{Width: 1}
		if dc.wide == gh.CellWideWide {
			out.Width = 2
		}

		switch dc.tag {
		case gh.CellContentCodepoint:
			if dc.cp != 0 {
				out.Content = string(dc.cp)
			} else {
				out.Content = " "
				out.Width = 1
			}
		case gh.CellContentCodepointGrapheme:
			if !cellsLoaded {
				if err := t.ri.Cells(t.rc); err != nil {
					out.Content = string(dc.cp)
					break
				}
				cellsLoaded = true
			}
			if err := t.rc.Select(uint16(x)); err == nil {
				if cps, err := t.rc.Graphemes(); err == nil && len(cps) > 0 {
					var sb strings.Builder
					for _, cp := range cps {
						sb.WriteRune(rune(cp))
					}
					out.Content = sb.String()
				} else {
					out.Content = string(dc.cp)
				}
			} else {
				out.Content = string(dc.cp)
			}
		default:
			out.Content = " "
			out.Width = 1
			out.Style = t.backgroundOnlyStyle(&bg, cell, dc.tag, nil)
		}

		if dc.styleID != 0 {
			out.Style = t.styleFor(dc.styleID, x)
		}
		if IsKittyPlaceholder(out.Content) && t.kittyPlaceholderMode == KittyPlaceholdersDrop {
			out.Content = " "
			out.Width = 1
		}
		if IsKittyPlaceholder(out.Content) {
			var left *uv.Cell
			if x > 0 {
				left = buf.CellAt(x-1, y)
			}
			rewriteKittyPlaceholder(out, left, t.kittyImageIDTranslator, &memo, x, y)
		}
		if dc.link {
			if uri := t.hyperlinkAt(x, y); uri != "" {
				out.Link = uv.Link{URL: LinkURI(uri)}
			}
		}
		buf.SetCell(x, y, out)
		if dc.prot {
			buf.setProtected(x, y, out.Width, true)
		}
	}
	for x := n; x < t.width; x++ {
		buf.SetCell(x, y, &uv.Cell{Content: " ", Width: 1})
	}
}

func (t *GhosttyTerminal) styleFor(styleID uint16, x int) uv.Style {
	if styleID == 0 {
		return uv.Style{}
	}
	if s, ok := t.styleCache[styleID]; ok {
		return s
	}
	if err := t.ri.Cells(t.rc); err != nil {
		return uv.Style{}
	}
	if err := t.rc.Select(uint16(x)); err != nil {
		return uv.Style{}
	}
	gs, err := t.rc.Style()
	if err != nil || gs == nil {
		return uv.Style{}
	}
	s := t.convertStyle(gs)
	t.styleCache[styleID] = s
	return s
}

func (t *GhosttyTerminal) convertStyle(gs *gh.Style) uv.Style {
	return t.convertStyleIn(gs, nil)
}

func (t *GhosttyTerminal) convertStyleIn(gs *gh.Style, pal *ghosttyPalette) uv.Style {
	var s uv.Style
	if gs.Bold() {
		s.Attrs |= uv.AttrBold
	}
	if gs.Faint() {
		s.Attrs |= uv.AttrFaint
	}
	if gs.Italic() {
		s.Attrs |= uv.AttrItalic
	}
	if gs.Blink() {
		s.Attrs |= uv.AttrBlink
	}
	if gs.Inverse() {
		s.Attrs |= uv.AttrReverse
	}
	if gs.Invisible() {
		s.Attrs |= uv.AttrConceal
	}
	if gs.Strikethrough() {
		s.Attrs |= uv.AttrStrikethrough
	}
	switch gs.Underline() {
	case gh.UnderlineSingle:
		s.Underline = ansi.UnderlineSingle
	case gh.UnderlineDouble:
		s.Underline = ansi.UnderlineDouble
	case gh.UnderlineCurly:
		s.Underline = ansi.UnderlineCurly
	case gh.UnderlineDotted:
		s.Underline = ansi.UnderlineDotted
	case gh.UnderlineDashed:
		s.Underline = ansi.UnderlineDashed
	}
	s.Fg = t.resolveStyleColor(gs.FgColor(), pal)
	s.Bg = t.resolveStyleColor(gs.BgColor(), pal)
	s.UnderlineColor = t.resolveStyleColor(gs.UnderlineColor(), pal)
	return s
}

func (t *GhosttyTerminal) resolveStyleColor(sc gh.StyleColor, pal *ghosttyPalette) color.Color {
	switch sc.Tag {
	case gh.StyleColorRGB:
		return color.RGBA{R: sc.RGB.R, G: sc.RGB.G, B: sc.RGB.B, A: 0xff}
	case gh.StyleColorPalette:
		i := int(sc.Palette)
		var c color.Color
		if pal != nil {
			c = pal[i]
		} else {
			c = t.paletteEntryLocked(i)
		}
		if c != nil {
			return c
		}
		if i < 16 {
			return ansi.BasicColor(uint8(i)) //nolint:gosec // bounded above
		}
		if i < 256 {
			return ansi.IndexedColor(uint8(i)) //nolint:gosec // bounded above
		}
		return nil
	default:
		return nil
	}
}

func (t *GhosttyTerminal) paletteEntryLocked(i int) color.Color {
	if i < 0 || i > 255 {
		return nil
	}
	if c := t.colors[i]; c != nil {
		return c
	}
	if i < 16 {
		return t.themePal[i]
	}
	return nil
}

type ghosttyPalette [256]color.Color

func (t *GhosttyTerminal) paletteLocked() *ghosttyPalette {
	var pal ghosttyPalette
	for i := range pal {
		pal[i] = t.paletteEntryLocked(i)
	}
	return &pal
}

func (t *GhosttyTerminal) hyperlinkAt(x, y int) string {
	if t.closed.Load() {
		return ""
	}
	ref, err := t.term.GridRef(gh.Point{Tag: gh.PointTagActive, X: uint16(x), Y: uint32(y)})
	if err != nil || ref == nil {
		return ""
	}
	uri, err := ref.HyperlinkURI()
	if err != nil {
		return ""
	}
	return uri
}

func (t *GhosttyTerminal) cursorLocked() (int, int) {
	t.syncLocked()
	return t.curX, t.curY
}

// CursorPosition implements [Terminal.CursorPosition].
func (t *GhosttyTerminal) CursorPosition() uv.Position {
	t.mu.Lock()
	defer t.mu.Unlock()
	x, y := t.cursorLocked()
	return uv.Pos(x, y)
}

// IsCursorHidden implements [Terminal.IsCursorHidden].
func (t *GhosttyTerminal) IsCursorHidden() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.curHidden
}

// CursorPen returns the SGR state in force, read straight from the library.
func (t *GhosttyTerminal) CursorPen() (uv.Style, uv.Link) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return uv.Style{}, uv.Link{}
	}
	t.flushRestoreLocked()
	gs, err := t.term.CursorSGRStyle()
	if err != nil || gs == nil {
		return uv.Style{}, uv.Link{}
	}
	link, _ := t.penExtrasLocked()
	return t.convertStyle(gs), link
}

// Bounds implements [Terminal.Bounds].
func (t *GhosttyTerminal) Bounds() uv.Rectangle {
	t.mu.Lock()
	defer t.mu.Unlock()
	return uv.Rect(0, 0, t.width, t.height)
}

// CellAt implements [Terminal.CellAt].
func (t *GhosttyTerminal) CellAt(x, y int) *uv.Cell {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.bufs[t.active].CellAt(x, y)
}

// MainCellAt implements [Terminal.MainCellAt].
func (t *GhosttyTerminal) MainCellAt(x, y int) *uv.Cell {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.bufs[0].CellAt(x, y)
}

// SetCell implements [Terminal.SetCell].
func (t *GhosttyTerminal) SetCell(x, y int, c *uv.Cell) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingRestore().setActiveCell(t.active, x, y, c)
}

// SetMainCell implements [Terminal.SetMainCell].
func (t *GhosttyTerminal) SetMainCell(x, y int, c *uv.Cell) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingRestore().setGridCell(0, x, y, c)
}

// Render implements [Terminal.Render].
func (t *GhosttyTerminal) Render() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.bufs[t.active].Render()
}

// String implements [Terminal.String].
func (t *GhosttyTerminal) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return uv.TrimSpace(StripSixelMarkers(t.bufs[t.active].String()))
}

// TailText mirrors the pure emulator: the last n non-empty screen rows,
// top-down.
func (t *GhosttyTerminal) TailText(n int) []string {
	if n <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	w, h := t.width, t.height
	if w <= 0 || h <= 0 {
		return nil
	}
	grid := t.bufs[t.active]

	m := min(n, h)
	var endsStack [64]int
	ends := endsStack[:0]
	if m > len(endsStack) {
		ends = make([]int, 0, m)
	}
	buf := make([]byte, 0, m*w)
	last := w
	for y := h - 1; y >= 0 && len(ends) < n; y-- {
		start := len(buf)
		if cap(buf)-start < last {
			nb := make([]byte, start, start+min(n-len(ends), y+1)*last)
			copy(nb, buf)
			buf = nb
		}
		for x := range w {
			if c := grid.CellAt(x, y); c != nil {
				buf = append(buf, CellText(c.Content)...)
			}
		}
		last = max(last, len(buf)-start)
		i := len(buf)
		for i > start && (buf[i-1] == ' ' || buf[i-1] == '\t') {
			i--
		}
		buf = buf[:i]
		if i > start {
			ends = append(ends, i)
		}
	}
	k := len(ends)
	out := make([]string, k)
	if k == 0 {
		return out
	}
	s := unsafe.String(unsafe.SliceData(buf), len(buf))
	prev := 0
	for i, end := range ends {
		out[k-1-i] = s[prev:end]
		prev = end
	}
	return out
}

// ReserveImageSpace scrolls to make room below the cursor for a graphics
// placement, with a blank pen so background-color erase does not paint the
// reserved rows, then leaves the cursor at column 0 under the image. All
// synthesized, because the grid is the library's.
func (t *GhosttyTerminal) ReserveImageSpace(rows, cols int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reserveImageSpaceLocked(rows, cols)
}

func (t *GhosttyTerminal) reserveImageSpaceLocked(rows, _ int) {
	if rows <= 0 || t.closed.Load() {
		return
	}
	_, startY := t.cursorLocked()
	height := t.height
	endY := startY + rows
	scrollCount := 0
	if endY > height {
		scrollCount = min(endY-height, maxReserveScroll)
	}
	finalY := startY + rows - scrollCount
	if finalY >= height {
		finalY = height - 1
	}

	var seq strings.Builder
	if scrollCount > 0 {
		pen, err := t.term.CursorSGRStyle()
		penSeq := ""
		if err == nil && pen != nil {
			s := t.convertStyle(pen)
			penSeq = penStyleSequence(&s)
		}
		seq.WriteString("\x1b[0m")
		fmt.Fprintf(&seq, "\x1b[%d;1H", height)
		for range scrollCount {
			seq.WriteString("\n")
		}
		seq.WriteString("\x1b[0m")
		seq.WriteString(penSeq)
	}
	fmt.Fprintf(&seq, "\x1b[%d;1H", finalY+1)
	t.term.VTWrite([]byte(seq.String()))
	t.gridStale = true
	t.scrollGeneration++
}

func (t *GhosttyTerminal) placeSixelLocked(rows, cols int, id uint32) {
	t.placeImageLocked(rows, cols, id, true)
}

// PlaceImageMarkers implements [Terminal.PlaceImageMarkers].
func (t *GhosttyTerminal) PlaceImageMarkers(rows, cols int, id uint32, move bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.placeImageLocked(rows, cols, id, move)
}

func (t *GhosttyTerminal) placeImageLocked(rows, cols int, id uint32, move bool) {
	if rows <= 0 || cols <= 0 || t.closed.Load() {
		return
	}
	startX, startY := t.cursorLocked()
	height, width := t.height, t.width
	scroll := 0
	if move && startY+rows+1 > height {
		scroll = min(startY+rows+1-height, height)
	}
	pen, err := t.term.CursorSGRStyle()
	penSeq := ""
	if err == nil && pen != nil {
		s := t.convertStyle(pen)
		penSeq = penStyleSequence(&s)
	}
	insert, _ := t.term.Mode(gh.ModeInsert)
	origin, _ := t.term.Mode(gh.ModeOrigin)

	var seq strings.Builder
	seq.WriteString("\x1b[0m")
	if insert {
		seq.WriteString("\x1b[4l")
	}
	if origin {
		seq.WriteString("\x1b[?6l")
	}
	if scroll > 0 {
		fmt.Fprintf(&seq, "\x1b[%dS", scroll)
	}
	t.term.VTWrite([]byte(seq.String()))
	seq.Reset()

	t.gridStale = true
	t.syncLocked()
	var buf *grid
	if t.activeAltLiveLocked() == (t.active == 1) {
		buf = t.bufs[t.active]
	}

	top := startY - scroll
	n := min(cols, width-startX)
	for r := range rows {
		y := top + r
		if y < 0 || y >= height || n <= 0 {
			continue
		}
		fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, startX+1)
		var last color.Color
		for c := range n {
			var bg color.Color
			if buf != nil {
				if cell := buf.CellAt(startX+c, y); cell != nil {
					bg = cell.Style.Bg
				}
			}
			if c == 0 || bg != last {
				seq.WriteString("\x1b[0m")
				if bg != nil {
					st := uv.Style{Bg: bg}
					seq.WriteString(penStyleSequence(&st))
				}
				last = bg
			}
			seq.WriteString(SixelMarker(id, r, c))
		}
	}
	seq.WriteString("\x1b[0m")
	if insert {
		seq.WriteString("\x1b[4h")
	}
	finalX, finalY := 0, min(startY+rows-scroll, height-1)
	if !move {
		finalX, finalY = startX, startY
	}
	t.restoreCursorLocked(&seq, finalX, finalY, origin, penSeq)
	t.term.VTWrite([]byte(seq.String()))
	t.gridStale = true
	t.scrollGeneration++
}

func (t *GhosttyTerminal) restoreCursorLocked(seq *strings.Builder, x, y int, origin bool, penSeq string) {
	if origin {
		seq.WriteString("\x1b[?6h")
		fmt.Fprintf(seq, "\x1b[%d;%dH", y-t.scrollRegion.Min.Y+1, x+1)
	} else {
		fmt.Fprintf(seq, "\x1b[%d;%dH", y+1, x+1)
	}
	seq.WriteString(penSeq)
}

// EraseImageMarkers implements [Terminal.EraseImageMarkers]: each run of
// matching marker cells on a row is written over with blanks in the
// background each cell had, the pen, modes and cursor put back after.
func (t *GhosttyTerminal) EraseImageMarkers(match func(id uint32) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return
	}
	t.gridStale = true
	t.syncLocked()
	if t.activeAltLiveLocked() != (t.active == 1) {
		return
	}
	buf := t.bufs[t.active]
	startX, startY := t.cursorLocked()
	pen, err := t.term.CursorSGRStyle()
	penSeq := ""
	if err == nil && pen != nil {
		s := t.convertStyle(pen)
		penSeq = penStyleSequence(&s)
	}
	insert, _ := t.term.Mode(gh.ModeInsert)
	origin, _ := t.term.Mode(gh.ModeOrigin)
	var seq strings.Builder
	any := false
	for y := range t.height {
		run := -1
		for x := 0; x <= t.width; x++ {
			hit := false
			var bg color.Color
			if x < t.width {
				if cell := buf.CellAt(x, y); cell != nil {
					if id, _, _, ok := ParseSixelMarker(cell.Content); ok && match(id) {
						hit, bg = true, cell.Style.Bg
					}
				}
			}
			if !hit {
				run = -1
				continue
			}
			if !any {
				seq.WriteString("\x1b[0m")
				if insert {
					seq.WriteString("\x1b[4l")
				}
				if origin {
					seq.WriteString("\x1b[?6l")
				}
				any = true
			}
			if run < 0 {
				fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, x+1)
				run = x
			}
			seq.WriteString("\x1b[0m")
			if bg != nil {
				st := uv.Style{Bg: bg}
				seq.WriteString(penStyleSequence(&st))
			}
			seq.WriteByte(' ')
		}
	}
	if !any {
		return
	}
	seq.WriteString("\x1b[0m")
	if insert {
		seq.WriteString("\x1b[4h")
	}
	t.restoreCursorLocked(&seq, startX, startY, origin, penSeq)
	t.term.VTWrite([]byte(seq.String()))
	t.gridStale = true
	t.scrollGeneration++
}
