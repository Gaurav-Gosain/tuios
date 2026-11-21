package vt

import (
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const maxClusterBytes = 64

var asciiStr [128]string

var symbolStr string

const (
	symbolFirst = 0x2000
	symbolEnd   = 0x2C00
)

func init() {
	for i := range asciiStr {
		asciiStr[i] = string(rune(i))
	}
	b := make([]byte, 0, (symbolEnd-symbolFirst)*3)
	for r := rune(symbolFirst); r < symbolEnd; r++ {
		b = utf8.AppendRune(b, r)
	}
	symbolStr = string(b)
}

type openGrapheme struct {
	active      bool
	x, y        int
	width       int
	left, right int
	base        string
	baseASCII   byte
}

func (o *openGrapheme) baseCluster() string {
	if o.baseASCII != 0 {
		return asciiStr[o.baseASCII]
	}
	return o.base
}

func (o *openGrapheme) arm(x, y, width, left, right int, ascii byte, base string) {
	o.active = true
	o.x, o.y = x, y
	o.width = width
	o.left, o.right = left, right
	o.baseASCII = ascii
	if o.base != "" || base != "" {
		o.base = base
	}
}

func (o *openGrapheme) disarm() {
	o.active = false
	o.baseASCII = 0
	if o.base != "" {
		o.base = ""
	}
}

func (e *Emulator) handlePrint(r rune) {
	if r >= ansi.SP && r < ansi.DEL {
		if len(e.grapheme) > 0 {
			e.flushGrapheme()
		}
		shifted := e.gsingle > 1 && e.gsingle < 4 && e.charsets[e.gsingle] != nil
		e.handleGrapheme(asciiStr[r], 1)

		if e.charsets[e.gl] == nil && !shifted {
			e.openGrapheme.arm(e.lastCellX, e.lastCellY, 1, e.lastCellLeft, e.lastCellRight, byte(r), "")
		} else {
			e.openGrapheme.disarm()
		}
	} else {
		if e.openGrapheme.active && len(e.grapheme) == 0 {
			e.grapheme = append(e.grapheme[:0], e.openGrapheme.baseCluster()...)
		}
		e.grapheme = utf8.AppendRune(e.grapheme, r)
		if e.openGrapheme.active {
			e.extendOpenGrapheme()
		}
	}
}

func (e *Emulator) flushGrapheme() {
	if e.openGrapheme.active {
		e.openGrapheme.disarm()
		e.grapheme = e.grapheme[:0]
		return
	}
	if len(e.grapheme) == 0 {
		return
	}
	e.renderGraphemeBuffer()
	e.grapheme = e.grapheme[:0]
}

func (e *Emulator) renderGraphemeBuffer() {
	method := ansi.GraphemeWidth
	buf := e.grapheme
	var run clusterRun
	for len(buf) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(buf, method)
		e.handleGrapheme(run.str(e, buf, len(cluster)), width)
		buf = buf[len(cluster):]
	}
}

const clusterTableLen = 1024

const maxInternBytes = 32

const maxRunMisses = 4

type clusterRun struct {
	misses  int
	rest    string
	restLen int
}

func (run *clusterRun) str(e *Emulator, buf []byte, n int) string {
	b := buf[:n]
	if r, size := utf8.DecodeRune(b); size == n {
		if r < utf8.RuneSelf {
			return asciiStr[r]
		}
		if r >= symbolFirst && r < symbolEnd {
			i := int(r-symbolFirst) * 3
			return symbolStr[i : i+3]
		}
	}
	if run.rest != "" {
		off := run.restLen - len(buf)
		return run.rest[off : off+n]
	}
	if n > maxInternBytes {
		return string(b)
	}
	if e.clusters == nil {
		e.clusters = new([clusterTableLen]string)
	}
	slot := &e.clusters[clusterHash(b)&(clusterTableLen-1)]
	if *slot == string(b) {
		return *slot
	}
	if run.misses >= maxRunMisses {
		run.rest = string(buf)
		run.restLen = len(buf)
		return run.rest[:n]
	}
	run.misses++
	*slot = string(b)
	return *slot
}

func clusterHash(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

func (e *Emulator) flushGraphemeAtWriteEnd() {
	if len(e.grapheme) == 0 || e.openGrapheme.active {
		return
	}

	method := ansi.GraphemeWidth
	buf := e.grapheme
	var run clusterRun
	var open string
	for len(buf) > 0 {
		raw, width := ansi.FirstGraphemeCluster(buf, method)
		cluster := run.str(e, buf, len(raw))
		res := e.handleGrapheme(cluster, width)
		buf = buf[len(raw):]
		if len(buf) > 0 {
			continue
		}
		switch res {
		case printedCell:
			open = cluster
			e.openGrapheme.arm(e.lastCellX, e.lastCellY, width, e.lastCellLeft, e.lastCellRight, 0, cluster)
		case printDeferred:
			if len(cluster) <= maxClusterBytes {
				open = cluster
			}
		case printConsumed:
		}
	}
	e.grapheme = append(e.grapheme[:0], open...)
}

func (e *Emulator) extendOpenGrapheme() {
	method := ansi.GraphemeWidth
	first, width := ansi.FirstGraphemeCluster(e.grapheme, method)
	if len(first) != len(e.grapheme) {
		e.openGrapheme.disarm()
		e.grapheme = append(e.grapheme[:0], e.grapheme[len(first):]...)
		return
	}

	if len(e.grapheme) > maxClusterBytes {
		_, n := utf8.DecodeLastRune(e.grapheme)
		e.grapheme = e.grapheme[:len(e.grapheme)-n]
		return
	}
	s := string(e.grapheme)
	cluster := s

	ox, oy := e.openGrapheme.x, e.openGrapheme.y
	left, right := e.openGrapheme.left, e.openGrapheme.right
	if width > right-ox {
		drawn := e.openGrapheme.baseCluster()
		e.openGrapheme.disarm()
		if e.autoWrapMode() {
			e.scr.SetCell(ox, oy, nil)
			e.scr.setCursor(ox, oy, false)
			e.atPhantom = false
			if e.handleGraphemeWithin(cluster, width, left, right) == printedCell {
				e.openGrapheme.arm(e.lastCellX, e.lastCellY, width, e.lastCellLeft, e.lastCellRight, 0, cluster)
			}
			return
		}
		e.grapheme = append(e.grapheme[:0], s[len(drawn):]...)
		return
	}

	cell := uv.Cell{
		Content: cluster,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}
	if IsKittyPlaceholder(cluster) && e.kittyPlaceholderMode == KittyPlaceholdersDrop {
		cell.Content = " "
	} else if IsKittyPlaceholder(cluster) {
		var left *uv.Cell
		if e.openGrapheme.x > 0 {
			left = e.scr.CellAt(e.openGrapheme.x-1, e.openGrapheme.y)
		}
		rewriteKittyPlaceholder(&cell, left, e.kittyImageIDTranslator, &e.kittyPlaceholderMemo, e.openGrapheme.x, e.openGrapheme.y)
	}
	e.scr.SetCell(e.openGrapheme.x, e.openGrapheme.y, &cell)
	e.markPrinted(e.openGrapheme.x, e.openGrapheme.y, width)
	e.openGrapheme.baseASCII = 0
	e.openGrapheme.base = cluster
	e.lastCluster, e.lastClusterWidth = cluster, width

	if width != e.openGrapheme.width {
		x, y := ox, oy
		if x+width >= right {
			e.parkedX, e.parkedY = x, y
			if e.autoWrapMode() {
				e.atPhantom = true
				x = right - 1
			} else {
				e.atPhantom = false
				x += width
			}
		} else {
			e.parkedX = -1
			e.atPhantom = false
			x += width
		}
		e.scr.setCursor(x, y, false)
		e.openGrapheme.width = width
	}
}

type printOutcome uint8

const (
	printedCell printOutcome = iota
	printConsumed
	printDeferred
)

func (e *Emulator) attachZeroWidth(content string) printOutcome {
	x, y := e.scr.CursorPosition()
	tx := x - 1
	if x == e.parkedX && y == e.parkedY {
		tx = x
	}
	if tx < 0 {
		return printDeferred
	}
	c := e.scr.CellAt(tx, y)
	if (c == nil || c.Content == "") && tx > 0 {
		if lead := e.scr.CellAt(tx-1, y); lead != nil && lead.Width == 2 {
			tx, c = tx-1, lead
		}
	}
	if c == nil || c.Content == "" {
		return printDeferred
	}

	cell := *c
	changed := false
	for _, r := range content {
		rs := string(r)
		if len(cell.Content)+len(rs) > maxClusterBytes {
			continue
		}
		if _, rw := ansi.FirstGraphemeCluster(rs, ansi.GraphemeWidth); rw != 0 {
			continue
		}
		joined := cell.Content + rs
		cl, cw := ansi.FirstGraphemeCluster(joined, ansi.GraphemeWidth)
		if len(cl) != len(joined) || cw != cell.Width {
			continue
		}
		cell.Content = joined
		changed = true
	}
	if changed {
		was := e.scr.buf.Protected(tx, y)
		e.scr.SetCell(tx, y, &cell)
		if was {
			e.scr.buf.setProtected(tx, y, max(cell.Width, 1), true)
		}
	}
	return printConsumed
}

func (e *Emulator) printASCIIRun(run []byte) {
	for len(run) > 0 {
		if e.atPhantom || e.insertMode() || e.gsingle != 0 || e.charsets[e.gl] != nil || e.cb.CursorPosition != nil {
			e.handlePrint(rune(run[0]))
			run = run[1:]
			continue
		}

		x, y := e.scr.CursorPosition()
		left, right := 0, e.scr.Width()
		limit := right
		if r := e.scr.ScrollRegion(); r.Min.X != 0 || r.Max.X != right {
			switch {
			case x >= r.Min.X && x < r.Max.X:
				left, right = r.Min.X, r.Max.X
				limit = right
			case x < r.Min.X:
				limit = r.Min.X
			}
		}
		n := min(len(run), limit-x)
		if n <= 0 {
			e.handlePrint(rune(run[0]))
			run = run[1:]
			continue
		}

		cell := uv.Cell{
			Width: 1,
			Style: e.scr.cursorPen(),
			Link:  e.scr.cursorLink(),
		}
		text := min(max(e.scr.buf.ext[y]-x, 0), n)
		if row := e.scr.buf.Row(y); row != nil && narrowRun(row[x:x+text]) {
			for k := range text {
				cell.Content = asciiStr[run[k]]
				row[x+k] = cell
			}
			if plainPen(&cell) {
				for k := text; k < n; k++ {
					row[x+k].Content = asciiStr[run[k]]
				}
			} else {
				for k := text; k < n; k++ {
					cell.Content = asciiStr[run[k]]
					row[x+k] = cell
				}
			}
			e.scr.buf.raiseExt(y, x+n)
			e.scr.buf.dropTail(y)
			e.scr.wideCol = false
			if e.scr.buf.prot != nil {
				e.scr.buf.clearProtected(y, x, x+n)
			}
		} else {
			for k := range n {
				cell.Content = asciiStr[run[k]]
				e.scr.SetCell(x+k, y, &cell)
			}
		}
		e.markPrinted(x, y, n)

		last := run[n-1]
		e.lastCluster, e.lastClusterWidth = asciiStr[last], 1
		e.lastCellX, e.lastCellY = x+n-1, y
		e.lastCellLeft, e.lastCellRight = left, right
		nx := x + n
		if nx >= right {
			e.parkedX, e.parkedY = x+n-1, y
			if e.autoWrapMode() {
				e.atPhantom = true
				nx = right - 1
			} else {
				e.atPhantom = false
			}
		} else {
			e.parkedX = -1
			e.atPhantom = false
		}
		e.scr.setCursor(nx, y, false)
		e.openGrapheme.arm(x+n-1, y, 1, left, right, last, "")
		run = run[n:]
	}
}

func plainPen(c *uv.Cell) bool {
	return c.Width == 1 && c.Style.Fg == nil && c.Style.Bg == nil && c.Style.UnderlineColor == nil &&
		c.Style.Attrs == 0 && c.Style.Underline == 0 &&
		c.Link.URL == "" && c.Link.Params == ""
}

func narrowRun(cells uv.Line) bool {
	for i := range cells {
		if cells[i].Width != 1 {
			return false
		}
	}
	return true
}

func (e *Emulator) handleGrapheme(content string, width int) printOutcome {
	if width == 0 {
		return e.attachZeroWidth(content)
	}

	x, _ := e.scr.CursorPosition()
	left, right := 0, e.scr.Width()
	if r := e.scr.ScrollRegion(); (r.Min.X != 0 || r.Max.X != right) && x >= r.Min.X && x < r.Max.X {
		left, right = r.Min.X, r.Max.X
	}
	return e.handleGraphemeWithin(content, width, left, right)
}

func (e *Emulator) handleGraphemeWithin(content string, width, left, right int) printOutcome {
	if len(content) > maxClusterBytes {
		n := maxClusterBytes
		for n > 0 && content[n]&0xC0 == 0x80 {
			n--
		}
		content = content[:n]
		if cl, w := ansi.FirstGraphemeCluster(content, ansi.GraphemeWidth); len(cl) == len(content) {
			width = w
		}
	}

	awm := e.autoWrapMode()
	cell := uv.Cell{
		Content: content,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}
	if IsKittyPlaceholder(content) && e.kittyPlaceholderMode == KittyPlaceholdersDrop {
		cell.Content, cell.Width = " ", 1
	} else if IsKittyPlaceholder(content) {
		var left *uv.Cell
		px, py := e.scr.CursorPosition()
		if px > 0 {
			left = e.scr.CellAt(px-1, py)
		}
		rewriteKittyPlaceholder(&cell, left, e.kittyImageIDTranslator, &e.kittyPlaceholderMemo, px, py)
	}

	x, y := e.scr.CursorPosition()

	if e.atPhantom && awm {
		e.noteSoftWrap(left, right)
		e.index()
		_, y = e.scr.CursorPosition()
		x = left
	}

	single := e.gsingle
	e.gsingle = 0
	if len(content) == 1 { //nolint:nestif
		var charset CharSet
		c := content[0]
		if single > 1 && single < 4 {
			charset = e.charsets[single]
		} else if c < 128 {
			charset = e.charsets[e.gl]
		} else {
			charset = e.charsets[e.gr]
		}

		if charset != nil {
			if r, ok := charset[c]; ok {
				cell.Content = r
				cell.Width = 1
			}
		}
	}

	if cell.Width > 1 && x+cell.Width > right {
		if !awm {
			_, sz := utf8.DecodeRuneInString(content)
			base, tail := content[:sz], content[sz:]
			_, bw := ansi.FirstGraphemeCluster(base, ansi.GraphemeWidth)
			if bw > 0 && x+bw <= right {
				e.handleGraphemeWithin(base, bw, left, right)
			} else {
				e.scr.setCursor(x, y, false)
			}
			if tail != "" {
				e.attachZeroWidth(tail)
			}
			return printConsumed
		}
		e.scr.SetCell(x, y, nil)
		e.noteSoftWrap(left, right)
		if e.scr.buf.SoftWrapped(y) {
			e.scr.buf.setPadded(y)
		}
		e.index()
		_, y = e.scr.CursorPosition()
		x = left
	}

	e.lastCluster, e.lastClusterWidth = content, width

	if e.insertMode() {
		e.scr.insertCellAt(x, y, cell.Width)
	}

	e.lastCellX, e.lastCellY = x, y
	e.lastCellLeft, e.lastCellRight = left, right
	e.scr.SetCell(x, y, &cell)
	e.markPrinted(x, y, cell.Width)

	parked := x+cell.Width >= right
	if parked {
		e.parkedX, e.parkedY = x, y
	} else {
		e.parkedX = -1
	}
	if awm && parked {
		e.atPhantom = true
		x = right - 1
	} else {
		e.atPhantom = false
		x += cell.Width
	}

	e.scr.setCursor(x, y, false)
	return printedCell
}
