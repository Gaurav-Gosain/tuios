package vt

import (
	"encoding/binary"
	"image/color"
	"reflect"
	"slices"
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DefaultScrollbackSize is the default number of lines to keep in the
// scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback is the ring of lines that have scrolled off the top of the
// screen. It is a ring so a push is O(1) once the ring is full.
//
// Lines are stored encoded, not as uv.Line. A uv.Cell is 112 bytes: a string
// header, three colour interfaces, a link of two strings and an int width. A
// scrollback line kept in that form costs terminal width times 112 bytes no
// matter what is on it, so at the default 10000 lines a 207-column pane held
// 232 MB the moment its ring filled, and the daemon and every attached client
// each held their own copy. An earlier packing brought a cell down to 24
// bytes, which is still 24 bytes for a plain letter: the same text as UTF-8
// is one byte.
//
// So a line is stored as the bytes of its text, with a style or a link
// written once where it changes rather than once per cell; see encodeLine.
// A plain line of a build log costs its length in bytes. A line is stored
// only up to its last cell that is not blank, and the ring's own slice of
// line headers grows as lines arrive rather than being allocated at the
// ring's capacity, so a pane that has printed nothing holds nothing. Reads
// decode back to uv.Line, padded to the width the line had, through a small
// cache so a render of a scrolled pane, which asks for the same line once
// per column, decodes each line once.
type Scrollback struct {
	lines     [][]byte
	maxLines  int
	head      int
	tail      int
	full      bool
	wraps     []rowFlag
	onTrim    func(int)
	holdTrims bool
	heldTrims int
	discard   bool

	colors   []color.Color
	colorIdx map[color.Color]uint32

	lineLinks []uv.Link

	cacheMu    sync.Mutex
	cache      map[int]uv.Line
	cacheGen   uint64
	cacheCells int
	gen        uint64
}

const internCap = 1 << 20

const (
	maxStoredLinkURL    = 2083
	maxStoredLinkParams = 256
)

const cacheCap = 256

const cacheCellCap = 1 << 16

const sbLineSlack = 16

const (
	sbStyle    = 0xFF
	sbLink     = 0xFE
	sbCell     = 0xFD
	sbGrapheme = 0xFC
	sbEmpty    = 0xFB
)

const (
	colorTagBits = 3
	colorTagMask = 1<<colorTagBits - 1

	colorNil      = 0
	colorBasic    = 1
	colorIndexed  = 2
	colorTrue     = 3
	colorRGB      = 4
	colorInterned = 5
)

type packedStyle struct {
	fg, bg, ul uint32
	attrs      uint8
	underline  uint8
}

// NewScrollback creates a scrollback ring of maxLines lines. Zero or less
// means DefaultScrollbackSize.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{maxLines: maxLines}
}

// SetOnTrim sets a callback that fires when the ring overwrites oldest lines.
func (sb *Scrollback) SetOnTrim(fn func(int)) {
	sb.onTrim = fn
}

// PushLine stores a copy of line as the newest scrollback line. The caller
// keeps line and may go on writing to it: nothing here aliases it.
//
// Into a full ring the push reuses the storage of the line it evicts when
// that is large enough, so a steady flood of similar lines allocates nothing.
func (sb *Scrollback) PushLine(line uv.Line) {
	if len(line) == 0 {
		return
	}

	n := len(line)
	for n > 0 && isBlankCell(&line[n-1]) {
		n--
	}
	sb.push(line[:n], len(line))
}

func (sb *Scrollback) pushTrimmed(line uv.Line, ext int) {
	if len(line) == 0 {
		return
	}
	n := min(ext, len(line))
	for n > 0 && isBlankCell(&line[n-1]) {
		n--
	}
	sb.push(line[:n], len(line))
}

// PushBlankLine stores a blank line of the given width as the newest
// scrollback line: what a row of the screen that nothing has written holds.
func (sb *Scrollback) PushBlankLine(width int) {
	if width <= 0 {
		return
	}
	sb.push(nil, width)
}

// Discard empties the ring and makes it drop every line pushed from now on,
// before the line is encoded. It is for an emulator whose history nobody
// reads: tuios gui-bridge runs one per pane only to follow titles, folders
// and modes, and its renderer keeps the history. Encoding each line that
// scrolled off was about a third of that process's work under a flood.
func (sb *Scrollback) Discard() {
	sb.Clear()
	sb.discard = true
}

func (sb *Scrollback) push(cells uv.Line, width int) {
	if sb.discard {
		if sb.holdTrims {
			sb.heldTrims++
		} else if sb.onTrim != nil {
			sb.onTrim(1)
		}
		return
	}
	inPlace := len(sb.lines) == sb.maxLines
	var buf []byte
	if inPlace {
		buf = sb.lines[sb.tail][:0]
	} else if len(sb.lines) < cap(sb.lines) {
		buf = sb.lines[:len(sb.lines)+1][len(sb.lines)][:0]
	}
	if cap(buf) < len(cells)+sbLineSlack && (cap(buf) == 0 || cap(buf) < len(cells)) {
		size := len(cells) + sbLineSlack
		if cap(buf) > 0 && sb.holdTrims {
			size = max(size, 2*cap(buf))
		}
		buf = make([]byte, 0, size)
	}
	buf = sb.encodeLine(buf, cells, width)

	if inPlace {
		sb.lines[sb.tail] = buf
		sb.wraps[sb.tail] = 0
	} else {
		if len(sb.lines) == cap(sb.lines) {
			grown := make([][]byte, len(sb.lines), min(sb.maxLines, max(2*cap(sb.lines), 64)))
			copy(grown, sb.lines)
			sb.lines = grown
		}
		sb.lines = append(sb.lines, buf)
		sb.wraps = append(sb.wraps, 0)
	}

	sb.tail = (sb.tail + 1) % sb.maxLines
	if sb.full {
		sb.head = (sb.head + 1) % sb.maxLines
		if sb.holdTrims {
			sb.heldTrims++
		} else if sb.onTrim != nil {
			sb.onTrim(1)
		}
	}
	if sb.tail == sb.head {
		sb.full = true
	}
	sb.gen++
}

func (sb *Scrollback) truncate(n int) {
	drop := sb.Len() - max(n, 0)
	if drop <= 0 {
		return
	}
	if len(sb.lines) < sb.maxLines {
		sb.lines = sb.lines[:len(sb.lines)-drop]
		sb.wraps = sb.wraps[:len(sb.wraps)-drop]
		sb.tail = len(sb.lines)
	} else {
		sb.tail = (sb.tail - drop + sb.maxLines) % sb.maxLines
		sb.full = false
	}
	sb.gen++
}

func isBlankCell(c *uv.Cell) bool {
	return c.Content == " " && c.Width == 1 &&
		c.Style.Fg == nil && c.Style.Bg == nil && c.Style.UnderlineColor == nil &&
		c.Style.Underline == 0 && c.Style.Attrs == 0 &&
		c.Link.URL == "" && c.Link.Params == ""
}

func isPlainASCIICell(c *uv.Cell) bool {
	return c.Width == 1 && len(c.Content) == 1 && c.Content[0] < utf8.RuneSelf &&
		c.Style.Fg == nil && c.Style.Bg == nil && c.Style.UnderlineColor == nil &&
		c.Style.Attrs == 0 && c.Style.Underline == 0 &&
		c.Link.URL == "" && c.Link.Params == ""
}

func (sb *Scrollback) encodeLine(buf []byte, cells uv.Line, width int) []byte {
	buf = binary.AppendUvarint(buf, uint64(width))
	var style packedStyle
	var link uv.Link
	clear(sb.lineLinks)
	sb.lineLinks = sb.lineLinks[:0]
	for i := 0; i < len(cells); i++ {
		if style == (packedStyle{}) && link == (uv.Link{}) {
			for i < len(cells) && isPlainASCIICell(&cells[i]) {
				buf = append(buf, cells[i].Content[0])
				i++
			}
			if i == len(cells) {
				break
			}
		}
		c := &cells[i]
		if st := sb.packStyle(&c.Style); st != style {
			style = st
			buf = append(buf, sbStyle)
			buf = binary.AppendUvarint(buf, uint64(st.fg))
			buf = binary.AppendUvarint(buf, uint64(st.bg))
			buf = binary.AppendUvarint(buf, uint64(st.ul))
			buf = append(buf, st.attrs, st.underline)
		}
		if c.Link != link {
			link = c.Link
			buf = sb.appendLink(buf, link)
		}
		if c.Width != 1 {
			buf = append(buf, sbCell, uint8(max(0, min(c.Width, 255))))
		}
		buf = sb.appendContent(buf, c.Content)
	}
	return buf
}

func (sb *Scrollback) appendLink(buf []byte, l uv.Link) []byte {
	buf = append(buf, sbLink)
	if l == (uv.Link{}) || LinkURIDecodedLen(l.URL) > maxStoredLinkURL || len(l.Params) > maxStoredLinkParams {
		return append(buf, 0)
	}
	for k := range sb.lineLinks {
		if sb.lineLinks[k] == l {
			return binary.AppendUvarint(buf, uint64(2+k))
		}
	}
	sb.lineLinks = append(sb.lineLinks, l)
	buf = append(buf, 1)
	buf = appendString(buf, l.URL)
	return appendString(buf, l.Params)
}

func appendString(buf []byte, s string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func readString(data []byte, i int) ([]byte, int, bool) {
	if i >= len(data) {
		return nil, i, false
	}
	n, m := binary.Uvarint(data[i:])
	if m <= 0 || n > uint64(len(data)-i-m) {
		return nil, i, false
	}
	i += m
	return data[i : i+int(n)], i + int(n), true
}

func readLink(data []byte, i int, seen []uv.Link) (uv.Link, []uv.Link, int, bool) {
	k, m := binary.Uvarint(data[i:])
	if m <= 0 {
		return uv.Link{}, seen, i, false
	}
	i += m
	switch {
	case k == 0:
		return uv.Link{}, seen, i, true
	case k == 1:
		url, i, ok := readString(data, i)
		if !ok {
			return uv.Link{}, seen, i, false
		}
		params, i, ok := readString(data, i)
		if !ok {
			return uv.Link{}, seen, i, false
		}
		l := uv.Link{URL: string(url), Params: string(params)}
		return l, append(seen, l), i, true
	case k-2 < uint64(len(seen)):
		return seen[k-2], seen, i, true
	}
	return uv.Link{}, seen, i, false
}

func skipLink(data []byte, i int) (int, bool) {
	k, m := binary.Uvarint(data[i:])
	if m <= 0 {
		return i, false
	}
	i += m
	if k != 1 {
		return i, true
	}
	var ok bool
	if _, i, ok = readString(data, i); !ok {
		return i, false
	}
	_, i, ok = readString(data, i)
	return i, ok
}

func (sb *Scrollback) appendContent(buf []byte, s string) []byte {
	if len(s) == 1 && s[0] < utf8.RuneSelf {
		return append(buf, s[0])
	}
	if s == "" {
		return append(buf, sbEmpty)
	}
	r, size := utf8.DecodeRuneInString(s)
	if size == len(s) && !(r == utf8.RuneError && size == 1) {
		return append(buf, s...)
	}
	buf = append(buf, sbGrapheme)
	return appendString(buf, s)
}

func (sb *Scrollback) packStyle(s *uv.Style) packedStyle {
	st := packedStyle{attrs: s.Attrs, underline: uint8(s.Underline)}
	if s.Fg != nil {
		st.fg = sb.packColor(s.Fg)
	}
	if s.Bg != nil {
		st.bg = sb.packColor(s.Bg)
	}
	if s.UnderlineColor != nil {
		st.ul = sb.packColor(s.UnderlineColor)
	}
	return st
}

func (sb *Scrollback) unpackStyle(st packedStyle) uv.Style {
	return uv.Style{
		Fg:             sb.unpackColor(st.fg),
		Bg:             sb.unpackColor(st.bg),
		UnderlineColor: sb.unpackColor(st.ul),
		Underline:      uv.Underline(st.underline),
		Attrs:          st.attrs,
	}
}

func (sb *Scrollback) packColor(c color.Color) uint32 {
	switch v := c.(type) {
	case nil:
		return colorNil
	case ansi.BasicColor:
		return colorBasic | uint32(v)<<colorTagBits
	case ansi.IndexedColor:
		return colorIndexed | uint32(v)<<colorTagBits
	case ansi.RGBColor:
		return colorTrue | (uint32(v.R)<<16|uint32(v.G)<<8|uint32(v.B))<<colorTagBits
	case color.RGBA:
		if v.A == 0xff {
			return colorRGB | (uint32(v.R)<<16|uint32(v.G)<<8|uint32(v.B))<<colorTagBits
		}
	}
	return sb.internColor(c)
}

func (sb *Scrollback) internColor(c color.Color) uint32 {
	comparable := reflect.TypeOf(c).Comparable()
	if comparable {
		if sb.colorIdx == nil {
			sb.colorIdx = make(map[color.Color]uint32)
		}
		if i, ok := sb.colorIdx[c]; ok {
			return colorInterned | i<<colorTagBits
		}
	}
	if len(sb.colors) >= internCap || !comparable {
		r, g, b, _ := c.RGBA()
		return colorRGB | ((r>>8)<<16|(g>>8)<<8|b>>8)<<colorTagBits
	}
	sb.colors = append(sb.colors, c)
	i := uint32(len(sb.colors) - 1)
	sb.colorIdx[c] = i
	return colorInterned | i<<colorTagBits
}

func (sb *Scrollback) unpackColor(v uint32) color.Color {
	payload := v >> colorTagBits
	switch v & colorTagMask {
	case colorBasic:
		return ansi.BasicColor(payload)
	case colorIndexed:
		return ansi.IndexedColor(payload)
	case colorTrue:
		return ansi.RGBColor{R: uint8(payload >> 16), G: uint8(payload >> 8), B: uint8(payload)}
	case colorRGB:
		return color.RGBA{R: uint8(payload >> 16), G: uint8(payload >> 8), B: uint8(payload), A: 0xff}
	case colorInterned:
		if payload < uint32(len(sb.colors)) {
			return sb.colors[payload]
		}
	}
	return nil
}

func (sb *Scrollback) markNewest(f rowFlag) {
	if sb.Len() == 0 {
		return
	}
	sb.wraps[(sb.tail-1+sb.maxLines)%sb.maxLines] = f
}

func (sb *Scrollback) setWrapped(index int, wrapped bool) {
	if index < 0 || index >= sb.Len() {
		return
	}
	sb.wraps[sb.slot(index)] = wrapFlag(wrapped)
}

// LineWrapped reports whether the line at index, oldest first, carried on to
// the next line by autowrap. The last line of the ring carries on to the
// screen's first row.
func (sb *Scrollback) LineWrapped(index int) bool {
	if index < 0 || index >= sb.Len() {
		return false
	}
	return sb.wraps[sb.slot(index)]&rowWrapped != 0
}

func (sb *Scrollback) lineFlags(index int) rowFlag {
	if index < 0 || index >= sb.Len() {
		return 0
	}
	return sb.wraps[sb.slot(index)]
}

// Len returns the number of lines in the ring.
func (sb *Scrollback) Len() int {
	if sb.full {
		return sb.maxLines
	}
	if sb.tail >= sb.head {
		return sb.tail - sb.head
	}
	return sb.maxLines - sb.head + sb.tail
}

func (sb *Scrollback) slot(index int) int {
	return (sb.head + index) % sb.maxLines
}

// Line returns the line at index, oldest first, decoded to its original
// width, or nil when index is out of range. The result is shared with later
// calls for the same index until the ring changes, so callers must not write
// to it.
func (sb *Scrollback) Line(index int) uv.Line {
	if index < 0 || index >= sb.Len() {
		return nil
	}
	sb.cacheMu.Lock()
	defer sb.cacheMu.Unlock()
	if sb.cacheGen != sb.gen {
		sb.clearCacheLocked()
	}
	if line, ok := sb.cache[index]; ok {
		return line
	}
	line := sb.decodeLine(sb.lines[sb.slot(index)])
	if len(sb.cache) >= cacheCap || sb.cacheCells+len(line) > cacheCellCap {
		sb.clearCacheLocked()
	}
	if sb.cache == nil {
		sb.cache = make(map[int]uv.Line)
	}
	sb.cache[index] = line
	sb.cacheCells += len(line)
	return line
}

func (sb *Scrollback) clearCacheLocked() {
	clear(sb.cache)
	sb.cacheCells = 0
	sb.cacheGen = sb.gen
}

func (sb *Scrollback) decodeLine(data []byte) uv.Line {
	width, n := binary.Uvarint(data)
	if n <= 0 {
		return nil
	}
	return sb.decodeInto(make(uv.Line, width), data[n:])
}

func lineWidth(data []byte) (width, n int) {
	w, n := binary.Uvarint(data)
	if n <= 0 {
		return 0, n
	}
	return int(w), n
}

func (sb *Scrollback) decodeRows(from, end int, block []uv.Cell, out []uv.Line) ([]uv.Line, []uv.Cell) {
	from, end = max(from, 0), min(end, sb.Len())
	if from >= end {
		return out[:0], block
	}
	total := 0
	for i := from; i < end; i++ {
		w, _ := lineWidth(sb.lines[sb.slot(i)])
		total += w
	}
	if cap(block) < total {
		block = make([]uv.Cell, total)
	}
	block = block[:total]
	out = slices.Grow(out[:0], end-from)
	at := 0
	for i := from; i < end; i++ {
		data := sb.lines[sb.slot(i)]
		w, n := lineWidth(data)
		line := block[at : at+w : at+w]
		at += w
		if n > 0 {
			sb.decodeInto(line, data[n:])
		}
		out = append(out, line)
	}
	return out, block
}

func (sb *Scrollback) decodeInto(line uv.Line, data []byte) uv.Line {
	x := 0
	n := 0
	var style uv.Style
	var link uv.Link
	var links []uv.Link
	i := n
	for i < len(data) && x < len(line) {
		switch data[i] {
		case sbStyle:
			var st packedStyle
			var ok bool
			st, i, ok = readStyle(data, i+1)
			if !ok {
				i = len(data)
				break
			}
			style = sb.unpackStyle(st)
		case sbLink:
			var ok bool
			link, links, i, ok = readLink(data, i+1, links)
			if !ok {
				i = len(data)
			}
		case sbCell:
			if i+1 >= len(data) {
				i = len(data)
				break
			}
			w := int(data[i+1])
			var content string
			var ok bool
			content, i, ok = sb.readContent(data, i+2)
			if !ok {
				i = len(data)
				break
			}
			line[x] = uv.Cell{Content: content, Style: style, Link: link, Width: w}
			x++
		default:
			var content string
			var ok bool
			content, i, ok = sb.readContent(data, i)
			if !ok {
				i = len(data)
				break
			}
			line[x] = uv.Cell{Content: content, Style: style, Link: link, Width: 1}
			x++
		}
	}
	for ; x < len(line); x++ {
		line[x] = uv.EmptyCell
	}
	return line
}

func readStyle(data []byte, i int) (packedStyle, int, bool) {
	var st packedStyle
	var vals [3]uint32
	for k := range vals {
		v, m := binary.Uvarint(data[i:])
		if m <= 0 {
			return st, i, false
		}
		vals[k] = uint32(v)
		i += m
	}
	if i+2 > len(data) {
		return st, i, false
	}
	st = packedStyle{fg: vals[0], bg: vals[1], ul: vals[2], attrs: data[i], underline: data[i+1]}
	return st, i + 2, true
}

func (sb *Scrollback) readContent(data []byte, i int) (string, int, bool) {
	if i >= len(data) {
		return "", i, false
	}
	switch data[i] {
	case sbEmpty:
		return "", i + 1, true
	case sbGrapheme:
		s, next, ok := readString(data, i+1)
		if !ok {
			return "", i, false
		}
		return string(s), next, true
	case sbStyle, sbLink, sbCell:
		return "", i, false
	}
	r, size := utf8.DecodeRune(data[i:])
	if r == utf8.RuneError && size <= 1 {
		return "", i, false
	}
	return string(data[i : i+size]), i + size, true
}

func skipContent(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return i, false
	}
	switch data[i] {
	case sbEmpty:
		return i + 1, true
	case sbGrapheme:
		_, next, ok := readString(data, i+1)
		if !ok {
			return i, false
		}
		return next, true
	case sbStyle, sbLink, sbCell:
		return i, false
	}
	r, size := utf8.DecodeRune(data[i:])
	if r == utf8.RuneError && size <= 1 {
		return i, false
	}
	return i + size, true
}

// Lines returns every line in the ring, oldest first, each decoded fresh.
func (sb *Scrollback) Lines() []uv.Line {
	length := sb.Len()
	if length == 0 {
		return nil
	}
	result := make([]uv.Line, length)
	for i := range length {
		result[i] = sb.decodeLine(sb.lines[sb.slot(i)])
	}
	return result
}

// Clear drops every line and the storage behind it.
func (sb *Scrollback) Clear() {
	count := sb.Len()
	sb.head = 0
	sb.tail = 0
	sb.full = false
	sb.lines = nil
	sb.wraps = nil
	sb.gen++
	if sb.onTrim != nil && count > 0 {
		sb.onTrim(count)
	}
}

// ClipHistoryRow is a scrollback line as it may be drawn in width columns.
//
// History keeps the width it was written at, deliberately: a resize reflows
// only the screen and the history lines it takes back (reflow.go), so that a
// resize costs what the screen holds and not what the history holds. A pane
// that narrowed since can then hold a double-width rune whose
// lead is in its last column. Drawn whole, that rune makes the row one column
// wider than the pane, and the compositor puts the extra column over the pane
// next door.
//
// The fix belongs where the row is drawn and not in the history. Blanking the
// rune in the stored line, which a resize once did, lost it for good: a pane
// that narrowed and widened again showed a space where the character was, and
// so did every capture of it. So the stored line keeps the rune, and a reader
// that puts a history row into a grid of the pane's width passes it through
// here first. The screenshot grid is one, and the client's pane renderer is
// another. The renderer needs it too: the frame does cut an overlong row at
// the pane's border, but that cut drops the whole wide cell, so the last
// column lost its background, the copy cursor and any selection. The cut cell becomes a blank that keeps its style, so a run of
// coloured background does not gain a notch.
//
// The line is returned as it is when nothing straddles the edge. Otherwise it
// is a copy, because the line can be the scrollback cache's own slice.
func ClipHistoryRow(line uv.Line, width int) uv.Line {
	if width <= 0 || width > len(line) {
		return line
	}
	edge := line[width-1]
	if edge.Width <= 1 {
		return line
	}
	out := make(uv.Line, width)
	copy(out, line[:width])
	out[width-1] = uv.Cell{Content: " ", Width: 1, Style: edge.Style}
	return out
}

// MaxLines returns the ring's capacity.
func (sb *Scrollback) MaxLines() int {
	return sb.maxLines
}

// SetMaxLines resizes the ring, keeping the newest lines that fit.
func (sb *Scrollback) SetMaxLines(maxLines int) {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	if maxLines == sb.maxLines {
		return
	}

	oldLen := sb.Len()
	newLen := min(oldLen, maxLines)
	var newLines [][]byte
	var newWraps []rowFlag
	if newLen > 0 {
		newLines = make([][]byte, newLen)
		newWraps = make([]rowFlag, newLen)
	}
	startIndex := oldLen - newLen
	for i := range newLen {
		newLines[i] = sb.lines[sb.slot(startIndex+i)]
		newWraps[i] = sb.wraps[sb.slot(startIndex+i)]
	}

	sb.lines = newLines
	sb.wraps = newWraps
	sb.maxLines = maxLines
	sb.head = 0
	sb.tail = newLen % maxLines
	sb.full = newLen == maxLines
	sb.gen++

	if oldLen > newLen && sb.onTrim != nil {
		sb.onTrim(oldLen - newLen)
	}
}

func extractLine(buf *grid, y, width int) uv.Line {
	line := make(uv.Line, width)
	for x := range width {
		if cell := buf.CellAt(x, y); cell != nil {
			line[x] = *cell
		} else {
			line[x] = uv.EmptyCell
		}
	}
	return line
}
