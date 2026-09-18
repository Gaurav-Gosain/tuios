package vt

import (
	"encoding/binary"
	"image/color"
	"reflect"
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
	// lines is the ring. While the ring is not full it holds only the lines
	// pushed so far, head is 0 and tail is len(lines); once it holds maxLines
	// entries a push overwrites the oldest.
	lines [][]byte
	// maxLines is the ring's capacity.
	maxLines int
	// head is the index of the oldest line.
	head int
	// tail is the index the next line lands at.
	tail int
	// full is set once tail has caught up with head.
	full bool
	// onTrim is called when the ring overwrites its oldest line. The
	// argument is the number of lines dropped.
	onTrim func(int)

	// Intern tables for what the encoding does not write inline: grapheme
	// clusters of more than one rune, hyperlinks, and colour values of a type
	// the packer does not know. Each is capped at internCap entries; past
	// the cap a cell degrades (first rune only, no link, colour reduced to
	// RGB) rather than growing without bound on a guest that manufactures
	// distinct values.
	graphemes   []string
	graphemeIdx map[string]uint32
	links       []uv.Link
	linkIdx     map[uv.Link]uint32
	colors      []color.Color
	colorIdx    map[color.Color]uint32

	// cache holds decoded lines by index for the current generation. Every
	// mutation bumps gen, which empties it on the next read. It is capped at
	// cacheCap entries so a walk of the whole ring cannot pin a decoded copy
	// of it.
	// cacheMu guards the cache map below. Line() is reachable from capture
	// paths that hold only terminalMu's read lock, so two captures can decode
	// and cache at once; without its own lock the map is written by both and
	// the runtime aborts the whole daemon on the concurrent map access.
	cacheMu  sync.Mutex
	cache    map[int]uv.Line
	cacheGen uint64
	gen      uint64
}

// internCap bounds each intern table. Past it, the packer degrades the cell
// instead of adding an entry.
const internCap = 1 << 20

// cacheCap bounds the decoded-line cache: a few screens of rows.
const cacheCap = 256

// sbLineSlack is the room a line's buffer gets beyond one byte per cell: the
// width header and a style change or two.
const sbLineSlack = 16

// Line encoding. A stored line is the line's width as a uvarint, then a
// token stream. The bytes 0xF8 to 0xFF never start a UTF-8 sequence, so a
// token that starts with any other byte is a plain cell: one rune of width
// one, in the current style with the current link. Everything else starts
// with one of these.
const (
	// sbStyle starts a style change: fg, bg and underline colour as uvarints
	// (see packColor), then the attribute byte and the underline style byte.
	// It applies to every cell after it.
	sbStyle = 0xFF
	// sbLink starts a link change: a uvarint that is 0 for no link and
	// otherwise 1+index into Scrollback.links.
	sbLink = 0xFE
	// sbCell starts a cell of some width other than one: the width as a
	// byte, then the cell's content token.
	sbCell = 0xFD
	// sbGrapheme is a content token: a uvarint index into
	// Scrollback.graphemes follows. At the top level it is a cell of width
	// one.
	sbGrapheme = 0xFC
	// sbEmpty is the content token for the empty string, which is what a
	// wide cell's spacer holds. At the top level it is a cell of width one.
	sbEmpty = 0xFB
)

// Colour packing: the low three bits say what the rest holds. Small values
// take one byte as a uvarint, which is what the tag being in the low bits is
// for.
const (
	colorTagBits = 3
	colorTagMask = 1<<colorTagBits - 1

	colorNil      = 0
	colorBasic    = 1
	colorIndexed  = 2
	colorTrue     = 3 // ansi.TrueColor, 24-bit RGB
	colorRGB      = 4 // color.RGBA with alpha 255, 24-bit RGB
	colorInterned = 5
)

// packedStyle is a uv.Style with its colours packed, so two styles can be
// compared without touching their colour interfaces. Comparing those with ==
// panics on a colour of a type that is not comparable, and colorEqual costs
// six RGBA calls.
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

// PushBlankLine stores a blank line of the given width as the newest
// scrollback line: what a row of the screen that nothing has written holds.
func (sb *Scrollback) PushBlankLine(width int) {
	if width <= 0 {
		return
	}
	sb.push(nil, width)
}

// push stores cells, the non-blank prefix of a line of the given width, as
// the newest line.
func (sb *Scrollback) push(cells uv.Line, width int) {
	var buf []byte
	if sb.full {
		buf = sb.lines[sb.tail][:0]
	}
	if cap(buf) < len(cells)+sbLineSlack {
		// Sized for a plain line up front, so the common line is one
		// allocation rather than the run of doublings append would make
		// from nothing. A styled or non-ASCII line grows past it once.
		buf = make([]byte, 0, len(cells)+sbLineSlack)
	}
	buf = sb.encodeLine(buf, cells, width)

	if sb.full {
		sb.lines[sb.tail] = buf
	} else {
		if len(sb.lines) == cap(sb.lines) {
			grown := make([][]byte, len(sb.lines), min(sb.maxLines, max(2*cap(sb.lines), 64)))
			copy(grown, sb.lines)
			sb.lines = grown
		}
		sb.lines = append(sb.lines, buf)
	}

	sb.tail = (sb.tail + 1) % sb.maxLines
	if sb.full {
		sb.head = (sb.head + 1) % sb.maxLines
		if sb.onTrim != nil {
			sb.onTrim(1)
		}
	}
	if sb.tail == sb.head {
		sb.full = true
	}
	sb.gen++
}

// isBlankCell reports whether c is a plain space: the cell a line's unused
// tail is made of, which the ring does not store.
func isBlankCell(c *uv.Cell) bool {
	return c.Content == " " && c.Width == 1 &&
		c.Style.Fg == nil && c.Style.Bg == nil && c.Style.UnderlineColor == nil &&
		c.Style.Underline == 0 && c.Style.Attrs == 0 &&
		c.Link.URL == "" && c.Link.Params == ""
}

// encodeLine appends the encoding of cells, a line of the given width whose
// blank tail has been trimmed, to buf.
func (sb *Scrollback) encodeLine(buf []byte, cells uv.Line, width int) []byte {
	buf = binary.AppendUvarint(buf, uint64(width))
	var style packedStyle
	var link uv.Link
	for i := range cells {
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
			buf = append(buf, sbLink)
			buf = binary.AppendUvarint(buf, uint64(sb.packLink(link)))
		}
		if c.Width != 1 {
			buf = append(buf, sbCell, uint8(max(0, min(c.Width, 255))))
		}
		buf = sb.appendContent(buf, c.Content)
	}
	return buf
}

// appendContent appends the content token for s: the rune itself when s is
// exactly one valid rune, otherwise an interned index or the empty marker.
func (sb *Scrollback) appendContent(buf []byte, s string) []byte {
	if len(s) == 1 && s[0] < utf8.RuneSelf {
		// Nearly every cell of a text screen, and the whole of a plain log.
		return append(buf, s[0])
	}
	if s == "" {
		return append(buf, sbEmpty)
	}
	r, size := utf8.DecodeRuneInString(s)
	if size == len(s) && !(r == utf8.RuneError && size == 1) {
		return append(buf, s...)
	}
	if sb.graphemeIdx == nil {
		sb.graphemeIdx = make(map[string]uint32)
	}
	if i, ok := sb.graphemeIdx[s]; ok {
		buf = append(buf, sbGrapheme)
		return binary.AppendUvarint(buf, uint64(i))
	}
	if len(sb.graphemes) >= internCap {
		// Table full: keep the first rune.
		if r == utf8.RuneError && size == 1 {
			return append(buf, ' ')
		}
		return utf8.AppendRune(buf, r)
	}
	sb.graphemes = append(sb.graphemes, s)
	i := uint32(len(sb.graphemes) - 1)
	sb.graphemeIdx[s] = i
	buf = append(buf, sbGrapheme)
	return binary.AppendUvarint(buf, uint64(i))
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

// packLink returns 0 for no link and otherwise 1+index into links.
func (sb *Scrollback) packLink(l uv.Link) uint32 {
	if l.URL == "" && l.Params == "" {
		return 0
	}
	if sb.linkIdx == nil {
		sb.linkIdx = make(map[uv.Link]uint32)
	}
	if i, ok := sb.linkIdx[l]; ok {
		return i + 1
	}
	if len(sb.links) >= internCap {
		return 0
	}
	sb.links = append(sb.links, l)
	i := uint32(len(sb.links) - 1)
	sb.linkIdx[l] = i
	return i + 1
}

func (sb *Scrollback) unpackLink(v uint64) uv.Link {
	if v == 0 || v > uint64(len(sb.links)) {
		return uv.Link{}
	}
	return sb.links[v-1]
}

// packColor packs the colour types the emulator produces into an integer and
// interns anything else.
func (sb *Scrollback) packColor(c color.Color) uint32 {
	switch v := c.(type) {
	case nil:
		return colorNil
	case ansi.BasicColor:
		return colorBasic | uint32(v)<<colorTagBits
	case ansi.IndexedColor:
		return colorIndexed | uint32(v)<<colorTagBits
	case ansi.TrueColor:
		return colorTrue | (uint32(v)&0xffffff)<<colorTagBits
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
		// No slot for it: keep what it looks like.
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
		return ansi.TrueColor(payload)
	case colorRGB:
		return color.RGBA{R: uint8(payload >> 16), G: uint8(payload >> 8), B: uint8(payload), A: 0xff}
	case colorInterned:
		if payload < uint32(len(sb.colors)) {
			return sb.colors[payload]
		}
	}
	return nil
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

// slot returns the ring slot of the line at index, oldest first.
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
	if sb.cacheGen != sb.gen || len(sb.cache) >= cacheCap {
		clear(sb.cache)
		sb.cacheGen = sb.gen
	}
	if line, ok := sb.cache[index]; ok {
		return line
	}
	line := sb.decodeLine(sb.lines[sb.slot(index)])
	if sb.cache == nil {
		sb.cache = make(map[int]uv.Line)
	}
	sb.cache[index] = line
	return line
}

// decodeLine decodes one stored line into a fresh uv.Line of its width. The
// decoder stops at the first token it cannot read whole and leaves the rest
// of the line blank, so a truncated record cannot take it out of bounds.
func (sb *Scrollback) decodeLine(data []byte) uv.Line {
	width, n := binary.Uvarint(data)
	if n <= 0 {
		return nil
	}
	line := make(uv.Line, width)
	x := 0
	var style uv.Style
	var link uv.Link
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
			v, m := binary.Uvarint(data[i+1:])
			if m <= 0 {
				i = len(data)
				break
			}
			i += 1 + m
			link = sb.unpackLink(v)
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

// readStyle reads the body of a style token starting at i and returns the
// index after it.
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

// readContent reads one content token starting at i and returns the index
// after it.
func (sb *Scrollback) readContent(data []byte, i int) (string, int, bool) {
	if i >= len(data) {
		return "", i, false
	}
	switch data[i] {
	case sbEmpty:
		return "", i + 1, true
	case sbGrapheme:
		v, m := binary.Uvarint(data[i+1:])
		if m <= 0 || v >= uint64(len(sb.graphemes)) {
			return "", i, false
		}
		return sb.graphemes[v], i + 1 + m, true
	case sbStyle, sbLink, sbCell:
		return "", i, false
	}
	r, size := utf8.DecodeRune(data[i:])
	if r == utf8.RuneError && size <= 1 {
		return "", i, false
	}
	if r < utf8.RuneSelf {
		// A one-byte string is served from the runtime's table.
		return string(rune(r)), i + size, true
	}
	return string(data[i : i+size]), i + size, true
}

// skipContent returns the index after the content token at i.
func skipContent(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return i, false
	}
	switch data[i] {
	case sbEmpty:
		return i + 1, true
	case sbGrapheme:
		_, m := binary.Uvarint(data[i+1:])
		if m <= 0 {
			return i, false
		}
		return i + 1 + m, true
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
	sb.gen++
	if sb.onTrim != nil && count > 0 {
		sb.onTrim(count)
	}
}

// blankWideRunesCutByTheEdge clears a double-width rune that a narrowing
// resize leaves in what is now the last column of a scrollback line, for the
// reason Screen.blankWideRunesCutByTheEdge gives.
func (sb *Scrollback) blankWideRunesCutByTheEdge(newWidth int) {
	x := newWidth - 1
	if x < 0 {
		return
	}
	for i := range sb.Len() {
		slot := sb.slot(i)
		w, ok := storedCellWidth(sb.lines[slot], x)
		if !ok || w <= 1 {
			continue
		}
		line := sb.decodeLine(sb.lines[slot])
		line[x].Content = " "
		line[x].Width = 1
		line[x].Link = uv.Link{}
		n := len(line)
		for n > 0 && isBlankCell(&line[n-1]) {
			n--
		}
		sb.lines[slot] = sb.encodeLine(sb.lines[slot][:0], line[:n], len(line))
		sb.gen++
	}
}

// storedCellWidth walks the token stream of a stored line to column x and
// returns that cell's width without decoding the line. It reports false when
// the line's stored cells end before x, which means the column is blank.
func storedCellWidth(data []byte, x int) (int, bool) {
	_, i := binary.Uvarint(data)
	if i <= 0 {
		return 0, false
	}
	col := 0
	for i < len(data) {
		var ok bool
		switch data[i] {
		case sbStyle:
			_, i, ok = readStyle(data, i+1)
			if !ok {
				return 0, false
			}
			continue
		case sbLink:
			_, m := binary.Uvarint(data[i+1:])
			if m <= 0 {
				return 0, false
			}
			i += 1 + m
			continue
		}
		w := 1
		if data[i] == sbCell {
			if i+1 >= len(data) {
				return 0, false
			}
			w = int(data[i+1])
			i, ok = skipContent(data, i+2)
		} else {
			i, ok = skipContent(data, i)
		}
		if !ok {
			return 0, false
		}
		if col == x {
			return w, true
		}
		col++
	}
	return 0, false
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
	if newLen > 0 {
		newLines = make([][]byte, newLen)
	}
	startIndex := oldLen - newLen // drop the oldest when shrinking
	for i := range newLen {
		newLines[i] = sb.lines[sb.slot(startIndex+i)]
	}

	sb.lines = newLines
	sb.maxLines = maxLines
	sb.head = 0
	sb.tail = newLen % maxLines
	sb.full = newLen == maxLines
	sb.gen++

	if oldLen > newLen && sb.onTrim != nil {
		sb.onTrim(oldLen - newLen)
	}
}

// extractLine copies row y of buf into a fresh line of the given width.
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
