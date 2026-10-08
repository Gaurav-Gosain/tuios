package vt

import (
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// maxClusterBytes caps how much text one cell can hold. Terminals bound this
// (xterm keeps a fixed number of combining characters per cell) because a
// guest can pour combining marks onto one base forever, and every path that
// grows a cell re-reads its whole content: unbounded content turns a mark
// flood quadratic. 64 bytes holds any real cluster (a four-person ZWJ family
// with skin tones is 25) with room to spare. The cap must be enforced the
// same way on every growth path, or the same bytes split across writes would
// keep a different prefix.
const maxClusterBytes = 64

// asciiStr holds the 128 single-byte ASCII strings so the printable-ASCII fast
// path in handlePrint can pass a package-lifetime string to handleGrapheme
// instead of allocating string(r) (which escapes to the heap) for every char.
var asciiStr [128]string

// symbolStr holds the UTF-8 of every rune from symbolFirst up to symbolEnd
// back to back, three bytes each, so clusterRun can hand any of them out
// as a substring. The range is general punctuation, arrows, maths, box
// drawing, blocks, shapes, dingbats and braille: what a TUI draws its borders,
// meters and graphs with, often one styled cell at a time.
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

// openGrapheme records a cluster that has been drawn while more of it may still
// be in flight, along with the cell it landed in. A cluster stays open until
// something that cannot be part of it arrives: another base character, a
// control code, or an escape sequence.
type openGrapheme struct {
	active bool
	x, y   int
	width  int
	// The margins the cluster was drawn under. handleGrapheme reads its
	// margins from the cursor before consuming a pending wrap, which is what
	// xterm does, so a continuation re-rendering the cell cannot recompute
	// them from the cell's own position: a cluster that wrapped in from
	// outside the margins was drawn under the screen's edges, not the
	// margins it landed inside.
	left, right int
	// base is the cluster as drawn. A continuation rune has to be appended to
	// it to find out whether the two are one cluster, and the drawing path does
	// not otherwise keep the text around.
	//
	// The single-byte case has its own field because it is the one the
	// printable-ASCII path takes for every character of every line a guest
	// prints. Storing a string there means a pointer write, and a pointer write
	// means a GC write barrier: measured over a plain log replay that alone ran
	// the whole emulator 4.5x slower. baseASCII is a scalar, so arming a
	// cluster costs a handful of register stores. It wins over base when set.
	base      string
	baseASCII byte
}

// baseCluster returns the text of the open cluster.
func (o *openGrapheme) baseCluster() string {
	if o.baseASCII != 0 {
		return asciiStr[o.baseASCII]
	}
	return o.base
}

// arm records a cluster as open at the cell it was drawn in. It touches no
// pointer field unless one is already set, keeping the printable-ASCII path
// free of write barriers.
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

// disarm closes the open cluster, again avoiding a pointer write in the common
// case that there is no string to release.
func (o *openGrapheme) disarm() {
	o.active = false
	o.baseASCII = 0
	if o.base != "" {
		o.base = ""
	}
}

// handlePrint handles printable characters.
func (e *Emulator) handlePrint(r rune) {
	if r >= ansi.SP && r < ansi.DEL {
		if len(e.grapheme) > 0 {
			// If we have a grapheme buffer, flush it before handling the ASCII character.
			e.flushGrapheme()
		}
		// handleGrapheme spends a pending single shift, so whether this
		// character was mapped through one has to be read first.
		shifted := e.gsingle > 1 && e.gsingle < 4 && e.charsets[e.gsingle] != nil
		e.handleGrapheme(asciiStr[r], 1)

		// Leave the character open as a cluster. An ASCII letter is a legal
		// base for combining marks and NFD text puts one straight after it:
		// `e` `U+0301` for an accented e, `1` `U+FE0F` `U+20E3` for a keycap.
		// Drawing the base and walking on stranded those marks in the next
		// cell, where the following character overwrote them, so the accent
		// silently disappeared. Re-arming here also retires whatever cluster
		// was open before, which is why the flush above stays conditional.
		//
		// A designated character set maps the byte to something else, and the
		// mapped text is what a combining mark would have to attach to.
		// Rebuilding the cell from the byte the guest sent would undo the
		// mapping, so with a set designated the cluster is closed instead.
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

// flushGrapheme flushes the current grapheme buffer, if any, and handles the
// grapheme as a single unit.
func (e *Emulator) flushGrapheme() {
	// An open cluster is already on screen; the arriving sequence closes it,
	// so retire the buffer instead of drawing it a second time. This runs even
	// with an empty buffer, because the ASCII path leaves a cluster open
	// without seeding one.
	if e.openGrapheme.active {
		e.openGrapheme.disarm()
		e.grapheme = e.grapheme[:0]
		return
	}
	if len(e.grapheme) == 0 {
		return
	}
	e.renderGraphemeBuffer()
	e.grapheme = e.grapheme[:0] // Reset the grapheme buffer.
}

// renderGraphemeBuffer draws every cluster held in the grapheme buffer. It does
// not clear the buffer; callers decide whether the trailing cluster stays open.
func (e *Emulator) renderGraphemeBuffer() {
	// We always use ansi.GraphemeWidth here to report accurate widths
	// and it's up to the caller to decide how to handle Unicode vs non-Unicode
	// modes.
	method := ansi.GraphemeWidth
	buf := e.grapheme
	var run clusterRun
	for len(buf) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(buf, method)
		e.handleGrapheme(run.str(e, buf, len(cluster)), width)
		buf = buf[len(cluster):]
	}
}

// clusterTableLen is the number of slots in Emulator.clusters, a power of
// two. 1024 slots are 16 KiB, made only for an emulator that prints text
// outside ASCII and the symbol block.
const clusterTableLen = 1024

// maxInternBytes is the longest cluster the table keeps. Every CJK
// character, accented letter and most emoji fit; a longer ZWJ sequence is
// rare enough to allocate.
const maxInternBytes = 32

// maxRunMisses is how many clusters of one buffered run may each get a
// string of their own before the rest of the run shares one allocation.
const maxRunMisses = 4

// clusterRun turns the clusters of one buffered run into the strings the
// cells drawn from them keep, allocating as little as it safely can.
//
// The buffer is reused, so a cell cannot keep a view of it. Text repeats (a
// CJK document, a status line redrawn every second, the same emoji in a list)
// so a cluster seen recently comes out of a small direct-mapped table at no
// cost. A cluster the table lacks gets a string of its own, which goes into
// the table. Novel text would then pay an allocation per character, more than
// the one string per run this replaced, so after maxRunMisses misses the rest
// of the run is copied once and later misses are substrings of that copy.
// Those are not put in the table: an entry would keep the whole copy alive.
type clusterRun struct {
	misses int
	// rest is the copy of the run from the cluster where it was made to the
	// run's end, and restLen is its length.
	rest    string
	restLen int
}

// str returns the first n bytes of buf, a cluster at the head of the
// unconsumed run, as a string a cell can keep.
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
		// Inside the copied tail: buf is a suffix of the run, as is rest.
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

// clusterHash is FNV-1a over a cluster's bytes.
func clusterHash(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

// flushGraphemeAtWriteEnd draws the buffered clusters when a Write runs out of
// bytes mid-cluster.
//
// A PTY read boundary can fall anywhere, including between a base character and
// its combining marks. The trailing cluster must be drawn now, because the user
// has to see the last character of a burst without waiting for more output, but
// it must also stay open: runes arriving in a later Write belong to that same
// cluster and have to re-render the cell they were split from. Closing the
// cluster here instead would drop the marks already drawn and leave the
// continuation sitting in the next cell.
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
			// handleGrapheme records where it actually drew, which is not
			// derivable from the cursor beforehand: a pending wrap makes it
			// index to the next line first.
			open = cluster
			e.openGrapheme.arm(e.lastCellX, e.lastCellY, width, e.lastCellLeft, e.lastCellRight, 0, cluster)
		case printDeferred:
			// Nothing reached the screen, so there is no cell to reopen;
			// the cluster stays buffered instead, and a continuation in the
			// next Write joins it there exactly as it would have unsplit. A
			// zero-width lead like a Prepend character only finds out what
			// it is once its base arrives. Past the cap it is dropped like
			// everywhere else, or a mark flood would be re-read whole at
			// every write boundary.
			if len(cluster) <= maxClusterBytes {
				open = cluster
			}
		case printConsumed:
			// Combined into an existing cell, or discarded for good. Keeping
			// it would apply it a second time at the next flush.
		}
	}
	// Keep only the open cluster so a continuation extends it and nothing else.
	e.grapheme = append(e.grapheme[:0], open...)
}

// extendOpenGrapheme re-renders the cluster left open by a previous Write, now
// that a continuation rune has arrived, into the cell it was originally drawn
// in rather than at the cursor.
func (e *Emulator) extendOpenGrapheme() {
	method := ansi.GraphemeWidth
	// Most arrivals do not extend the cluster: after a styled non-ASCII
	// character the cluster stays open across the SGR, so every next
	// character is tested against it. The test runs on the byte buffer
	// itself, and only a rune that does extend it pays for a string.
	first, width := ansi.FirstGraphemeCluster(e.grapheme, method)
	if len(first) != len(e.grapheme) {
		// The new rune began a fresh cluster instead of extending the open one.
		// Close the open cluster and leave the remainder buffered for the
		// normal path.
		e.openGrapheme.disarm()
		e.grapheme = append(e.grapheme[:0], e.grapheme[len(first):]...)
		return
	}

	if len(e.grapheme) > maxClusterBytes {
		// A continuation past the cap is dropped, the way every growth path
		// drops it; the buffer stays capped, so this re-reads a bounded
		// cluster per rune instead of an ever-growing one.
		_, n := utf8.DecodeLastRune(e.grapheme)
		e.grapheme = e.grapheme[:len(e.grapheme)-n]
		return
	}
	s := string(e.grapheme)
	cluster := s

	// A continuation can widen the cluster, and the cell it is already sitting
	// in may not have room: a presentation selector arriving after its base
	// was drawn in the last column. The whole-write path never leaves the
	// wide cluster there, so the split path cannot either. The margins are
	// the ones the base was drawn under, not the ones its cell now sits in.
	ox, oy := e.openGrapheme.x, e.openGrapheme.y
	left, right := e.openGrapheme.left, e.openGrapheme.right
	if width > right-ox {
		drawn := e.openGrapheme.baseCluster()
		e.openGrapheme.disarm()
		if e.autoWrapMode() {
			// Redraw the whole cluster from the cell its base was drawn in,
			// with the wrap it would have taken arriving unsplit; ghostty
			// moves the widened cluster to the next line the same way.
			e.scr.SetCell(ox, oy, nil)
			e.scr.setCursor(ox, oy, false)
			e.atPhantom = false
			if e.handleGraphemeWithin(cluster, width, left, right) == printedCell {
				e.openGrapheme.arm(e.lastCellX, e.lastCellY, width, e.lastCellLeft, e.lastCellRight, 0, cluster)
			}
			return
		}
		// No wrap to move to. The base keeps its cell and the continuation
		// runes fall back to the zero-width attach rules, which is what the
		// unsplit write does with a wide cluster it cannot place.
		e.grapheme = append(e.grapheme[:0], s[len(drawn):]...)
		return
	}

	cell := uv.Cell{
		Content: cluster,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}
	// Blanked or rewritten as handleGraphemeWithin does it, so a placeholder
	// whose marks arrive in a later write draws the same cell.
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
	// The marks are part of the character now, so a repeat has to carry them.
	e.lastCluster, e.lastClusterWidth = cluster, width

	// A continuation can change the cluster's width (a variation selector
	// turns a narrow base wide); recompute the cursor from the cluster's own
	// cell with the same margin rules the draw path uses, so following output
	// still lands after it and a cluster grown flush against the margin
	// parks and arms the pending wrap exactly as it would have unsplit.
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

// printOutcome says what handleGrapheme did with a cluster, which a caller
// leaving state across a Write boundary has to know: only a stored cell can
// be reopened, only an unconsumed cluster may stay buffered.
type printOutcome uint8

const (
	// printedCell: stored as a cell at the cursor.
	printedCell printOutcome = iota
	// printConsumed: combined into an existing cell, or discarded for good.
	printConsumed
	// printDeferred: nothing happened yet; the cluster may still grow into
	// something printable.
	printDeferred
)

// attachZeroWidth handles a zero-width cluster that arrives with no open
// cluster to extend: a combining mark after a control or a cursor move, a
// bidi control, a mark at the start of a row.
//
// Terminals attach these to the cell just written (ghostty and xterm both
// combine with the cell before the cursor, or with the cursor's own cell
// under a pending wrap) and drop them when there is nothing there: at
// column 0, over a never-written cell, or when the code point cannot form
// one cluster with the cell's content (a bidi control breaks the cluster
// where a combining mark extends it). Storing them as cells of their own
// would give the row more cells than columns, and Render, which emits
// nothing for them, would shift everything after one column left.
func (e *Emulator) attachZeroWidth(content string) printOutcome {
	x, y := e.scr.CursorPosition()
	tx := x - 1
	if x == e.parkedX && y == e.parkedY {
		// The cursor is still standing on the cell it last drew (a print at
		// the right margin, wrapped or not), so that cell is the base.
		tx = x
	}
	if tx < 0 {
		return printDeferred
	}
	c := e.scr.CellAt(tx, y)
	if (c == nil || c.Content == "") && tx > 0 {
		// The cell before the cursor may be the continuation of a wide
		// character; the mark belongs on its lead.
		if lead := e.scr.CellAt(tx-1, y); lead != nil && lead.Width == 2 {
			tx, c = tx-1, lead
		}
	}
	if c == nil || c.Content == "" {
		// Nothing to combine with yet. The cluster may still be completed by
		// a later write (a Prepend character measures zero until its base
		// arrives), so it stays buffered rather than dropped.
		return printDeferred
	}

	// One rune at a time, with a final verdict per rune, because that is the
	// only shape that gives the same screen however the run was split across
	// writes: each code point's fate depends only on the cell as it stands,
	// never on the runes still in flight. A rune joins if the cell still
	// reads as a single cluster of the same width with it appended. A rune
	// that would break the cluster (a bidi control) or change the measured
	// width (a keycap or presentation selector on a narrow base) is dropped:
	// the cell cannot grow without eating its neighbour, and the grid never
	// lies about width.
	cell := *c
	changed := false
	for _, r := range content {
		rs := string(r)
		if len(cell.Content)+len(rs) > maxClusterBytes {
			continue
		}
		if _, rw := ansi.FirstGraphemeCluster(rs, ansi.GraphemeWidth); rw != 0 {
			// A rune that occupies columns on its own (the emoji after a
			// joiner) is dropped rather than folded in: arriving at a
			// cluster boundary instead it would start a cell of its own,
			// and its fate must not depend on where a write boundary fell.
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
		// The mark joins a character already printed, which keeps the
		// protection it was printed with.
		was := e.scr.buf.Protected(tx, y)
		e.scr.SetCell(tx, y, &cell)
		if was {
			e.scr.buf.setProtected(tx, y, max(cell.Width, 1), true)
		}
	}
	return printConsumed
}

// printASCIIRun prints a run of printable ASCII bytes that arrived in the
// ground state. It draws as many of them as fit before the right edge in one
// pass, with one cell built for the run and one cursor move at the end,
// and hands the rest back to the per-character path.
//
// The per-character path does a lot per byte that is the same for every byte
// of a run: it reads the margins and the modes, builds a cell from the pen,
// records the last cluster, moves the cursor and fires its callback, and arms
// the open cluster. Only the last byte's bookkeeping survives to the next
// byte, so the run pays for it once. When the row exists and every cell the
// run overwrites is one column wide, the cells are stored straight into the
// row. Otherwise the writes go through Screen.SetCell one column at a time,
// which is what keeps a double-width character the run lands on, and the lazy
// allocation of a row nothing has written, handled exactly as before.
//
// Anything that makes one byte differ from the next goes to handlePrint
// instead: a pending wrap, insert mode, a designated character set, or a
// cursor callback that expects to see every step.
func (e *Emulator) printASCIIRun(run []byte) {
	for len(run) > 0 {
		if e.atPhantom || e.insertMode() || e.gsingle != 0 || e.charsets[e.gl] != nil || e.cb.CursorPosition != nil {
			e.handlePrint(rune(run[0]))
			run = run[1:]
			continue
		}

		x, y := e.scr.CursorPosition()
		left, right := 0, e.scr.Width()
		// limit is where this pass stops. It is the right edge, except for
		// a cursor to the left of the margins: the per-character path reads
		// the margins afresh for every character, so a run that starts
		// outside them and walks in has them apply from the first column
		// inside. The pass stops at that column and the next one picks the
		// margins up.
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
		if row := e.scr.buf.Row(y); row != nil && narrowRun(row[x:x+n]) {
			// Every cell being overwritten is one column wide, so no
			// double-width character is cut, and Screen.SetCell and
			// uv.Line.Set would each come down to this one store.
			for k := range n {
				cell.Content = asciiStr[run[k]]
				row[x+k] = cell
			}
			// Written behind the grid's back, so its extent is raised here
			// (see grid.ext), and the row's tail dropped (see grid.tail):
			// the shell has repainted the row.
			e.scr.buf.raiseExt(y, x+n)
			e.scr.buf.dropTail(y)
			e.scr.wideCol = false
			// And so is its protection, which SetCell would have cleared.
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

		// The bookkeeping handleGraphemeWithin does for the last character
		// of the run; every earlier character's is overwritten by the next.
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

// narrowRun reports whether every cell is exactly one column wide: neither the
// lead of a double-width character nor one of its continuation cells.
func narrowRun(cells uv.Line) bool {
	for i := range cells {
		if cells[i].Width != 1 {
			return false
		}
	}
	return true
}

// handleGrapheme handles UTF-8 graphemes.
func (e *Emulator) handleGrapheme(content string, width int) printOutcome {
	if width == 0 {
		return e.attachZeroWidth(content)
	}

	// Where the line ends and where a wrap lands. With DECLRMM set they are the
	// horizontal margins rather than the screen edges: wrapping at the right
	// margin is the whole reason a guest asks for one, and a terminal that
	// accepts the mode and then runs to the edge has given it nothing. A cursor
	// parked outside the margins keeps the screen's own edges, which is what
	// xterm does.
	x, _ := e.scr.CursorPosition()
	left, right := 0, e.scr.Width()
	if r := e.scr.ScrollRegion(); (r.Min.X != 0 || r.Max.X != right) && x >= r.Min.X && x < r.Max.X {
		left, right = r.Min.X, r.Max.X
	}
	return e.handleGraphemeWithin(content, width, left, right)
}

// handleGraphemeWithin is handleGrapheme with the line's edges already
// decided. It exists so a continuation re-rendering a cluster from a previous
// Write can replay the exact margins the cluster was drawn under.
func (e *Emulator) handleGraphemeWithin(content string, width, left, right int) printOutcome {
	if len(content) > maxClusterBytes {
		// A cluster over the cap keeps its head; the marks past it go the
		// way the attach path drops them.
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
	// A placeholder cell on a host that cannot draw it is a missing-glyph box
	// where a picture should be, so it is stored as a blank. It still takes
	// its cell: the application counted it as one and writes what follows
	// the image where that count puts it. Dropping the rune outright pulled
	// the rest of the row left and hung its row and column marks on the
	// character before the image (issue 292). The ghostty backend blanks the
	// same cell on the way out.
	if IsKittyPlaceholder(content) && e.kittyPlaceholderMode == KittyPlaceholdersDrop {
		cell.Content, cell.Width = " ", 1
	} else if IsKittyPlaceholder(content) {
		// A kitty placeholder cell names its image in its foreground colour,
		// and the name the guest used is not the one the host knows the image
		// by. The rewrite happens here, on the way into the grid, so both of
		// this backend's readers (its own Render and the per-cell path in the
		// app) see the translated cell without either having to know about it.
		// Spelling out the row and column here, while the row is still
		// whole, means clipping the left of it later cannot orphan the rest.
		// See kitty_placeholder.go.
		var left *uv.Cell
		px, py := e.scr.CursorPosition()
		if px > 0 {
			left = e.scr.CellAt(px-1, py)
		}
		rewriteKittyPlaceholder(&cell, left, e.kittyImageIDTranslator, &e.kittyPlaceholderMemo, px, py)
	}

	x, y := e.scr.CursorPosition()

	if e.atPhantom && awm {
		// moves cursor down similar to [Terminal.linefeed] except it doesn't
		// respects [ansi.LNM] mode.
		// This will reset the phantom state i.e. pending wrap state.
		e.noteSoftWrap(left, right)
		e.index()
		_, y = e.scr.CursorPosition()
		x = left
	}

	// Handle character set mappings. A single shift applies to exactly one
	// character, so it is spent here whatever the character is: a multi-byte
	// cluster the sets cannot map still uses it up, or the next ASCII byte
	// would be drawn from G2 or G3.
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

	// A double-width cluster needs both of its cells on the same row. Written
	// into the last column it leaves half a character hanging off the edge,
	// which the buffer refuses, so the guest's character disappears without a
	// trace: CJK text loses a character wherever it happens to meet the right
	// margin. xterm and ghostty both blank the column that cannot hold it and
	// wrap the cluster whole.
	if cell.Width > 1 && x+cell.Width > right {
		if !awm {
			// Nothing to wrap to. A cluster wide from its first rune (a CJK
			// character) is discarded whole and the cell keeps what it
			// already held, which is what ghostty does. A cluster a selector
			// widened has a base that fits on its own, so the base is drawn
			// as it would have been arriving first. Either way the zero-width
			// tail falls back to the attach rules. The split-write path
			// reaches this state one rune at a time, so anything else here
			// would make the screen depend on where a read boundary fell.
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

	// Recorded before the character set mapping is undone by a repeat: REP
	// repeats what the guest sent, and the designated set is still in force
	// when it does.
	e.lastCluster, e.lastClusterWidth = content, width

	// Insert mode (IRM) opens room for the character rather than overwriting
	// what is there, and a double-width cluster opens two columns rather than
	// one. terminfo reaches this through smir/rmir, so it runs under ordinary
	// curses programs and not only under a conformance suite.
	if e.insertMode() {
		e.scr.insertCellAt(x, y, cell.Width)
	}

	e.lastCellX, e.lastCellY = x, y
	e.lastCellLeft, e.lastCellRight = left, right
	e.scr.SetCell(x, y, &cell)
	e.markPrinted(x, y, cell.Width)

	// Pending wrap: the cursor stays on the character just drawn and the wrap
	// happens only when the next one arrives, so that a line ending exactly at
	// the margin does not scroll until there is something to put on the next
	// line. A wide cluster ending flush against the margin has to arm it too,
	// or the next character lands on that cluster's own second cell and eats
	// the character already there.
	//
	// parked records the same condition without the autowrap gate: whether or
	// not the line will wrap, the cursor is left standing on the cell just
	// drawn, and a zero-width arrival has to know that to combine with the
	// right cell. ghostty keeps its pending-wrap flag this way.
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

	// NOTE: We don't reset the phantom state here, we handle it up above.
	e.scr.setCursor(x, y, false)
	return printedCell
}
