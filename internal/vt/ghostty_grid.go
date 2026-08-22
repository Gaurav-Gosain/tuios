//go:build ghostty

package vt

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	gh "go.mitchellh.com/libghostty"
)

// This file keeps the uv shadow grid in step with libghostty's render state.
// The sync runs on demand: Write marks the grid stale, the first read after
// that walks the dirty rows. Each cell costs one Go memory read thanks to the
// packed-cell decoder; only grapheme cells, hyperlinks and style-cache misses
// call into the library.

// ghosttyCellDecoder decodes packed cell values using the bit layout the
// library publishes at runtime via TypeJSON. The layout is explicitly not
// ABI-stable, so it is read from the manifest rather than hardcoded; when the
// manifest cannot be parsed the decoder is disabled and cells are read
// through cgo getters instead.
type ghosttyCellDecoder struct {
	ok                bool
	tagLSB, tagMask   uint64
	cpLSB, cpMask     uint64
	styleLSB          uint64
	styleMask         uint64
	wideLSB, wideMask uint64
	linkBit           uint64
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
		// Some manifest versions nest types differently; give up and use
		// the getter path.
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
	// The codepoint lives inside the content union; its lsb is the union's
	// lsb because the CODEPOINT arm places it at offset 0.
	var content struct {
		LSB   uint64 `json:"lsb"`
		Width uint64 `json:"width"`
		Arms  map[string]struct {
			Bits map[string]field `json:"bits"`
		} `json:"arms"`
	}
	ok5 := false
	if rawContent, okC := cell.Bits["content"]; okC {
		if err := json.Unmarshal(rawContent, &content); err == nil {
			if arm, okA := content.Arms["CODEPOINT"]; okA {
				if cp, okB := arm.Bits["codepoint"]; okB {
					d.cpLSB = content.LSB + cp.LSB
					d.cpMask = (1 << cp.Width) - 1
					ok5 = true
				}
			}
		}
	}
	if !(ok1 && ok2 && ok3 && ok4 && ok5) {
		return d
	}
	d.tagLSB, d.tagMask = tag.LSB, (1<<tag.Width)-1
	d.styleLSB, d.styleMask = styleID.LSB, (1<<styleID.Width)-1
	d.wideLSB, d.wideMask = wide.LSB, (1<<wide.Width)-1
	d.linkBit = link.LSB
	d.ok = true
	return d
}

type decodedCell struct {
	tag     gh.CellContentTag
	cp      rune
	styleID uint16
	wide    gh.CellWide
	link    bool
}

func (d *ghosttyCellDecoder) decode(v uint64) decodedCell {
	return decodedCell{
		tag:     gh.CellContentTag((v >> d.tagLSB) & d.tagMask),
		cp:      rune((v >> d.cpLSB) & d.cpMask),
		styleID: uint16((v >> d.styleLSB) & d.styleMask),
		wide:    gh.CellWide((v >> d.wideLSB) & d.wideMask),
		link:    (v>>d.linkBit)&1 != 0,
	}
}

// decodeCellSlow reads the same fields through cgo getters, for builds whose
// manifest the decoder does not understand.
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
	return out
}

// syncLocked brings the shadow grid and cursor cache up to date. Call with
// mu held.
func (t *GhosttyTerminal) syncLocked() {
	if t.closed.Load() {
		return
	}
	t.flushRestoreLocked()
	if !t.gridStale {
		return
	}
	t.gridStale = false

	// Screen switches first: dirty tracking restarts on the new screen.
	activeAlt := t.IsAltScreen()
	idx := 0
	if activeAlt {
		idx = 1
	}
	t.active = idx
	buf := t.bufs[idx]

	if err := t.rs.Update(t.term); err != nil {
		return
	}
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

	// Cursor cache.
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

// syncRowLocked converts one dirty row into uv cells.
func (t *GhosttyTerminal) syncRowLocked(buf *uv.Buffer, y int) {
	view, err := t.ri.CellsRaw()
	if err != nil {
		return
	}
	n := view.Len()
	cellsLoaded := false
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
			// uv.Line.Set manages wide-cell placeholders itself when the
			// leading cell lands; writing one explicitly makes Set blank
			// the wide cell it belongs to.
			continue
		case gh.CellWideSpacerHead:
			// End-of-row spacer: the wide glyph wrapped to the next line,
			// so this position holds nothing and must not keep whatever
			// the previous frame left there.
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
			// Multi-codepoint cluster: fetch the tail through the cells
			// iterator. Rare enough that the extra calls do not matter.
			if !cellsLoaded {
				if err := t.ri.Cells(t.rc); err != nil {
					out.Content = string(dc.cp)
					break
				}
				cellsLoaded = true
			}
			if err := t.rc.Select(uint16(x)); err == nil {
				if cps, err := t.rc.Graphemes(); err == nil && len(cps) > 0 {
					// Graphemes returns the full cluster, base included.
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
			// Background-only cells carry no text.
			out.Content = " "
			out.Width = 1
		}

		out.Style = t.styleFor(dc.styleID, x)
		if dc.link {
			if uri := t.hyperlinkAt(x, y); uri != "" {
				out.Link = uv.Link{URL: uri}
			}
		}
		buf.SetCell(x, y, out)
	}
	// Rows narrower than the shadow (after a resize race) blank the rest.
	for x := n; x < t.width; x++ {
		buf.SetCell(x, y, &uv.Cell{Content: " ", Width: 1})
	}
	_ = utf8.RuneLen // keep import if unused paths change
}

// styleFor converts a style ID to uv form through the cache. x is the cell
// column for the miss path, which reads the full style from the current row's
// cells iterator.
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

// convertStyle maps a libghostty style onto uv.Style with the pure
// emulator's color rules: palette indices resolve through the guest's OSC 4
// overrides first and the user's theme second, and pass through as indices
// when nothing claimed them.
func (t *GhosttyTerminal) convertStyle(gs *gh.Style) uv.Style {
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
	s.Fg = t.resolveStyleColor(gs.FgColor())
	s.Bg = t.resolveStyleColor(gs.BgColor())
	s.UnderlineColor = t.resolveStyleColor(gs.UnderlineColor())
	return s
}

// resolveStyleColor applies the pure emulator's paletteEntry rule.
func (t *GhosttyTerminal) resolveStyleColor(sc gh.StyleColor) color.Color {
	switch sc.Tag {
	case gh.StyleColorRGB:
		return color.RGBA{R: sc.RGB.R, G: sc.RGB.G, B: sc.RGB.B, A: 0xff}
	case gh.StyleColorPalette:
		i := int(sc.Palette)
		if c := t.paletteEntryLocked(i); c != nil {
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

// paletteEntryLocked mirrors the pure emulator: guest OSC 4 first, theme
// second, nil when the slot is the host terminal's to decide.
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

// hyperlinkAt fetches the hyperlink URI for an active-screen cell.
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

// cursorLocked returns the cursor position, syncing first.
func (t *GhosttyTerminal) cursorLocked() (int, int) {
	t.syncLocked()
	return t.curX, t.curY
}

func (t *GhosttyTerminal) CursorPosition() uv.Position {
	t.mu.Lock()
	defer t.mu.Unlock()
	x, y := t.cursorLocked()
	return uv.Pos(x, y)
}

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
	gs, err := t.term.CursorStyle()
	if err != nil || gs == nil {
		return uv.Style{}, uv.Link{}
	}
	// The pen's hyperlink is not exposed; snapshot restore loses an open
	// OSC 8 link on the pen (not on cells), which the differential harness
	// tolerates.
	return t.convertStyle(gs), uv.Link{}
}

func (t *GhosttyTerminal) Bounds() uv.Rectangle {
	t.mu.Lock()
	defer t.mu.Unlock()
	return uv.Rect(0, 0, t.width, t.height)
}

func (t *GhosttyTerminal) CellAt(x, y int) *uv.Cell {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.bufs[t.active].CellAt(x, y)
}

func (t *GhosttyTerminal) MainCellAt(x, y int) *uv.Cell {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.bufs[0].CellAt(x, y)
}

func (t *GhosttyTerminal) SetCell(x, y int, c *uv.Cell) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingRestore().setActiveCell(t.active, x, y, c)
}

func (t *GhosttyTerminal) SetMainCell(x, y int, c *uv.Cell) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingRestore().setGridCell(0, x, y, c)
}

func (t *GhosttyTerminal) Render() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return t.bufs[t.active].Render()
}

func (t *GhosttyTerminal) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	return uv.TrimSpace(t.bufs[t.active].String())
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
	buf := t.bufs[t.active]
	out := make([]string, 0, n)
	var b strings.Builder
	for y := t.height - 1; y >= 0 && len(out) < n; y-- {
		b.Reset()
		for x := 0; x < t.width; x++ {
			if c := buf.CellAt(x, y); c != nil {
				b.WriteString(c.Content)
			}
		}
		if line := strings.TrimRight(b.String(), " \t"); line != "" {
			out = append(out, line)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
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
		scrollCount = min(endY-height, height)
	}
	finalY := startY + rows - scrollCount
	if finalY >= height {
		finalY = height - 1
	}

	var seq strings.Builder
	if scrollCount > 0 {
		// Save the pen, blank it, scroll from the bottom row, restore.
		pen, err := t.term.CursorStyle()
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
