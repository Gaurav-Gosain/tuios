//go:build ghostty

package vt

import (
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

const historyChunkRows = 64

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
			_, _ = scratch.WriteString("\x1b7\x1b[0m\x1b[2J\x1b8\x1b[H")
			y = 0
		} else {
			_, _ = scratch.WriteString("\r\n")
			y++
		}
	}
	if row != n {
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
		if last := &lines[i][width-1]; last.Content == " " && last.Width == 1 &&
			ghosttyRowWrap(src, gh.Point{Tag: gh.PointTagHistory, Y: uint32(from + i)}) &&
			ghosttySpacerHead(src, gh.Point{Tag: gh.PointTagHistory, X: uint16(width - 1), Y: uint32(from + i)}) {
			*last = uv.Cell{}
		}
	}
	return lines, exact
}

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

const historyReadBlock = 512

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
