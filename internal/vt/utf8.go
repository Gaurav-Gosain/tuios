package vt

import (
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// kittyPlaceholderChar is the base character used by kitty's unicode
// placeholder image protocol (U=1). Apps like yazi emit this character
// with combining diacritical marks to encode image-id/row/column.
// tuios handles kitty graphics via a separate overlay layer, so these
// placeholder characters should be invisible in the text buffer.
const kittyPlaceholderChar = 0x10EEEE

// maxClusterBytes caps how much text one cell can hold. Terminals bound this
// - xterm keeps a fixed number of combining characters per cell - because a
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

func init() {
	for i := range asciiStr {
		asciiStr[i] = string(rune(i))
	}
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
	// Suppress kitty unicode placeholder characters. They would show as
	// garbled text because tuios renders images via its own passthrough
	// layer, not by interpreting placeholder cells.
	if r == kittyPlaceholderChar {
		return
	}
	if r >= ansi.SP && r < ansi.DEL {
		if len(e.grapheme) > 0 {
			// If we have a grapheme buffer, flush it before handling the ASCII character.
			e.flushGrapheme()
		}
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
		if e.charsets[e.gl] == nil && e.gsingle == 0 {
			e.openGrapheme.arm(e.lastCellX, e.lastCellY, 1, e.lastCellLeft, e.lastCellRight, byte(r), "")
		} else {
			e.openGrapheme.disarm()
		}
	} else {
		if e.openGrapheme.active && len(e.grapheme) == 0 {
			e.grapheme = append(e.grapheme[:0], []rune(e.openGrapheme.baseCluster())...)
		}
		e.grapheme = append(e.grapheme, r)
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
	graphemes := string(e.grapheme)
	for len(graphemes) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(graphemes, method)
		e.handleGrapheme(cluster, width)
		graphemes = graphemes[len(cluster):]
	}
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
	graphemes := string(e.grapheme)
	var open string
	for len(graphemes) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(graphemes, method)
		res := e.handleGrapheme(cluster, width)
		graphemes = graphemes[len(cluster):]
		if len(graphemes) > 0 {
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
	e.grapheme = append(e.grapheme[:0], []rune(open)...)
}

// extendOpenGrapheme re-renders the cluster left open by a previous Write, now
// that a continuation rune has arrived, into the cell it was originally drawn
// in rather than at the cursor.
func (e *Emulator) extendOpenGrapheme() {
	method := ansi.GraphemeWidth
	s := string(e.grapheme)
	cluster, width := ansi.FirstGraphemeCluster(s, method)
	if len(cluster) != len(s) {
		// The new rune began a fresh cluster instead of extending the open one.
		// Close the open cluster and leave the remainder buffered for the
		// normal path.
		e.openGrapheme.disarm()
		e.grapheme = append(e.grapheme[:0], []rune(s[len(cluster):])...)
		return
	}

	if len(s) > maxClusterBytes {
		// A continuation past the cap is dropped, the way every growth path
		// drops it; the buffer stays capped, so this re-reads a bounded
		// cluster per rune instead of an ever-growing one.
		e.grapheme = e.grapheme[:len(e.grapheme)-1]
		return
	}

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
		e.grapheme = append(e.grapheme[:0], []rune(s[len(drawn):])...)
		return
	}

	cell := uv.Cell{
		Content: cluster,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}
	e.scr.SetCell(e.openGrapheme.x, e.openGrapheme.y, &cell)
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
// Terminals attach these to the cell just written - ghostty and xterm both
// combine with the cell before the cursor, or with the cursor's own cell
// under a pending wrap - and drop them when there is nothing there: at
// column 0, over a never-written cell, or when the code point cannot form
// one cluster with the cell's content (a bidi control breaks the cluster
// where a combining mark extends it). Storing them as cells of their own
// instead gave the row more cells than columns, and Render, which emits
// nothing for them, shifted everything after one column left.
func (e *Emulator) attachZeroWidth(content string) printOutcome {
	x, y := e.scr.CursorPosition()
	tx := x - 1
	if x == e.parkedX && y == e.parkedY {
		// The cursor is still standing on the cell it last drew - a print at
		// the right margin, wrapped or not - so that cell is the base.
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
		// a later write - a Prepend character measures zero until its base
		// arrives - so it stays buffered rather than dropped.
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
			// A rune that occupies columns on its own - the emoji after a
			// joiner - is dropped rather than folded in: arriving at a
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
		e.scr.SetCell(tx, y, &cell)
	}
	return printConsumed
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

	x, y := e.scr.CursorPosition()

	if e.atPhantom && awm {
		// moves cursor down similar to [Terminal.linefeed] except it doesn't
		// respects [ansi.LNM] mode.
		// This will reset the phantom state i.e. pending wrap state.
		e.index()
		_, y = e.scr.CursorPosition()
		x = left
	}

	// Handle character set mappings
	if len(content) == 1 { //nolint:nestif
		var charset CharSet
		c := content[0]
		if e.gsingle > 1 && e.gsingle < 4 {
			charset = e.charsets[e.gsingle]
			e.gsingle = 0
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
			// Nothing to wrap to. A cluster wide from its first rune - a CJK
			// character - is discarded whole and the cell keeps what it
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
