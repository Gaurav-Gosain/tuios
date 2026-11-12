//go:build ghostty

package vt

import (
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// The library has no bulk read of history rows: readHistoryLine reads a row
// one cell at a time, a GridRef and a Cell for each, and finding the GridRef
// walks the history's pages, so the cost grows with its depth. A copy-mode
// search over 10,000 lines took 515 ms that way, over 100,000 lines 20 s.
//
// historyTextFast reads the text of a range of rows with one call instead:
// the library's formatter writes the range in VT form, and a scratch pure Go
// emulator turns that back into cells. It is exact about text and widths, not
// about styles: the formatter writes nothing for a cell that has a style and
// no text, so selection and attach, which need styles, keep the cell-by-cell
// read. Search needs only text and widths.
//
// The two emulators agree on what the formatter's sequences do; where they
// may not agree is how text is cut into cells. So a row is read cell by cell
// when its text is not the text the formatter wrote, when its cells are not
// cut as the library's grapheme rules and widths (gh.UnicodeGraphemeWidth)
// would cut them, or when a cell holding a cluster is not the library's cell
// in codepoints and width.
//
// ghostty_history_rows_test.go checks, over vtgen scripts and the Unicode
// sweeps, that every row read this way has the text and widths of the
// cell-by-cell read.

// historyChunkRows is how many rows the scratch emulator holds before it is
// cleared for the next ones.
const historyChunkRows = 64

// historyTextFast returns history rows [from, end) of src, a terminal whose
// screen is width columns wide, exact in text and widths only, and for each
// whether it is.
func (t *GhosttyTerminal) historyTextFast(src *gh.Terminal, from, end, width int) ([]uv.Line, []bool) {
	n := end - from
	if n <= 0 || width <= 0 {
		return nil, nil
	}
	lines := make([]uv.Line, n)
	exact := make([]bool, n)
	text, ok := formatHistoryVT(src, from, end, width)
	if !ok {
		return lines, exact
	}
	scratch := NewEmulator(width, historyChunkRows)
	scratch.DiscardScrollback()
	// The scratch emulator's own replies (none are expected) go nowhere.
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := scratch.Read(buf); err != nil {
				return
			}
		}
	}()
	defer func() { _ = scratch.Close() }()

	y := 0
	row := 0
	for row < n {
		cut := strings.Index(text, "\r\n")
		last := cut < 0
		part := text
		if !last {
			part = text[:cut]
		}
		_, _ = scratch.WriteString(part)
		lines[row] = readScratchRow(scratch, y, width)
		exact[row] = sameText(part, lines[row]) && t.rowMatchesLibrary(lines[row])
		row++
		if last {
			break
		}
		text = text[cut+2:]
		if y == historyChunkRows-1 {
			// Clear the screen for the next rows without losing the pen the
			// formatter's stream carries from row to row.
			_, _ = scratch.WriteString("\x1b7\x1b[0m\x1b[2J\x1b8\x1b[H")
			y = 0
		} else {
			_, _ = scratch.WriteString("\r\n")
			y++
		}
	}
	if row != n {
		// The formatter wrote a different number of rows: trust none.
		clear(exact)
		return lines, exact
	}
	for i := range lines {
		if !exact[i] {
			continue
		}
		if !t.clustersMatchLibrary(src, from+i, lines[i]) {
			exact[i] = false
			continue
		}
		// A wide character that did not fit at the end of a row leaves a
		// spacer there, which the cell-by-cell read gives as an empty cell
		// and the formatter writes as nothing, so the scratch row has a
		// blank. Only a row that ends in a blank and carries on can hold one.
		if last := &lines[i][width-1]; last.Content == " " && last.Width == 1 &&
			ghosttyRowWrap(src, gh.Point{Tag: gh.PointTagHistory, Y: uint32(from + i)}) &&
			ghosttySpacerHead(src, gh.Point{Tag: gh.PointTagHistory, X: uint16(width - 1), Y: uint32(from + i)}) {
			*last = uv.Cell{}
		}
	}
	return lines, exact
}

// formatHistoryVT is history rows [from, end) of src in VT form, a row a
// line, with no extras, every blank kept and soft wraps kept as breaks.
func formatHistoryVT(src *gh.Terminal, from, end, width int) (string, bool) {
	start, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, X: 0, Y: uint32(from)})
	if err != nil || start == nil {
		return "", false
	}
	stop, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, X: uint16(width - 1), Y: uint32(end - 1)})
	if err != nil || stop == nil {
		return "", false
	}
	f, err := gh.NewFormatter(src,
		gh.WithFormatterFormat(gh.FormatterFormatVT),
		gh.WithFormatterSelection(&gh.Selection{Start: *start, End: *stop}),
		gh.WithFormatterTrim(false),
		gh.WithFormatterUnwrap(false),
		gh.WithFormatterExtras(gh.FormatterExtrasNone))
	if err != nil {
		return "", false
	}
	defer f.Close()
	text, err := f.FormatString()
	if err != nil {
		return "", false
	}
	return text, true
}

// readScratchRow copies row y of the scratch emulator.
func readScratchRow(e *Emulator, y, width int) uv.Line {
	line := make(uv.Line, width)
	for x := range width {
		if c := e.CellAt(x, y); c != nil {
			line[x] = *c
		} else {
			line[x] = uv.Cell{Content: " ", Width: 1}
		}
	}
	return line
}

// rowMatchesLibrary reports whether the scratch emulator cut the row into
// cells as the library does: each cell one grapheme cluster by the library's
// rules, of the library's width. A row of single ASCII characters needs no
// check.
func (t *GhosttyTerminal) rowMatchesLibrary(line uv.Line) bool {
	ascii := true
	for i := range line {
		c := &line[i]
		if c.Width == 0 && c.Content == "" {
			continue
		}
		if len(c.Content) != 1 || c.Content[0] >= 0x80 || c.Width != 1 {
			ascii = false
			break
		}
	}
	if ascii {
		return true
	}
	var cps []uint32
	var cells []int
	for i := range line {
		c := &line[i]
		if c.Width == 0 && c.Content == "" {
			continue
		}
		cells = append(cells, i)
		for _, r := range c.Content {
			cps = append(cps, uint32(r))
		}
	}
	at := 0
	for _, i := range cells {
		c := &line[i]
		size := len([]rune(c.Content))
		if size == 0 || at+size > len(cps) {
			return false
		}
		consumed, w := gh.UnicodeGraphemeWidth(cps[at:])
		if consumed != size || int(w) != c.Width {
			return false
		}
		at += size
	}
	return true
}

// historyReadBlock is how many rows eachHistoryText formats at a time, so a
// search over a deep history holds one block of decoded rows, not all of them.
const historyReadBlock = 512

// eachHistoryText calls fn with history rows [from, end) of src in order,
// exact in text and widths: read through historyTextFast a block at a time,
// with each row it is not sure of read cell by cell. It stops when fn returns
// false. The caller holds the terminal's lock.
func (t *GhosttyTerminal) eachHistoryText(src *gh.Terminal, from, end, width int, fn func(index int, line uv.Line) bool) {
	for start := from; start < end; start += historyReadBlock {
		stop := min(start+historyReadBlock, end)
		lines, exact := t.historyTextFast(src, start, stop, width)
		for i := start; i < stop; i++ {
			line := lines[i-start]
			if !exact[i-start] {
				line = t.readHistoryLineLocked(src, i)
			}
			if !fn(i, line) {
				return
			}
		}
	}
}

// sameText reports whether the scratch row holds the text the formatter wrote
// for it, spaces aside: the pure emulator drops some text the library keeps (a
// DEL, combining marks past its limit, a character it does not print), and the
// cell-by-cell read would show that text.
func sameText(vt string, line uv.Line) bool {
	var want []rune
	for i := 0; i < len(vt); {
		c := vt[i]
		if c == 0x1b {
			var set bool
			i, _ = skipVTEscape(vt, i, &set)
			continue
		}
		if c < 0x20 {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(vt[i:])
		i += size
		if r != ' ' {
			want = append(want, r)
		}
	}
	k := 0
	for x := range line {
		for _, r := range line[x].Content {
			if r == ' ' {
				continue
			}
			if k >= len(want) || want[k] != r {
				return false
			}
			k++
		}
	}
	return k == len(want)
}

// clustersMatchLibrary compares every cell of the row that holds more than
// one codepoint with the library's own cell. The library's cells do not
// always follow its own segmentation: a cluster cut by a write boundary or a
// cursor move stays cut, and the formatter writes its codepoints in a row, so
// the scratch emulator joins them. Such cells are rare outside emoji and
// combining marks, so reading them one by one costs little.
func (t *GhosttyTerminal) clustersMatchLibrary(src *gh.Terminal, index int, line uv.Line) bool {
	for x := range line {
		c := line[x].Content
		if len(c) <= 1 || utf8.RuneCountInString(c) <= 1 {
			continue
		}
		ref, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, X: uint16(x), Y: uint32(index)})
		if err != nil || ref == nil {
			return false
		}
		cps, err := ref.Graphemes()
		if err != nil || len(cps) == 0 {
			return false
		}
		// The library can hold a cluster at a width its own table would not
		// give it, when the codepoints arrived apart: a VS16 after its base
		// leaves the base one cell wide.
		cell, err := ref.Cell()
		if err != nil || cell == nil {
			return false
		}
		var dc decodedCell
		if t.dec.ok {
			dc = t.dec.decode(cell.PackedValue())
		} else {
			dc = decodeCellSlow(cell)
		}
		w := 1
		if dc.wide == gh.CellWideWide {
			w = 2
		}
		if w != line[x].Width {
			return false
		}
		if utf8.RuneCountInString(c) != len(cps) {
			return false
		}
		i := 0
		for _, r := range c {
			if uint32(r) != cps[i] {
				return false
			}
			i++
		}
	}
	return true
}
