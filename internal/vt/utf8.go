package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// kittyPlaceholderChar is the base character used by kitty's unicode
// placeholder image protocol (U=1). Apps like yazi emit this character
// with combining diacritical marks to encode image-id/row/column.
// tuios handles kitty graphics via a separate overlay layer, so these
// placeholder characters should be invisible in the text buffer.
const kittyPlaceholderChar = 0x10EEEE

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
func (o *openGrapheme) arm(x, y, width int, ascii byte, base string) {
	o.active = true
	o.x, o.y = x, y
	o.width = width
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
			e.openGrapheme.arm(e.lastCellX, e.lastCellY, 1, byte(r), "")
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
		e.handleGrapheme(cluster, width)
		graphemes = graphemes[len(cluster):]
		if len(graphemes) == 0 {
			// handleGrapheme records where it actually drew, which is not
			// derivable from the cursor beforehand: a pending wrap makes it
			// index to the next line first.
			open = cluster
			e.openGrapheme.arm(e.lastCellX, e.lastCellY, width, 0, cluster)
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

	// A continuation can widen the cluster, and the cell it is already sitting
	// in may not have room. Half a character at the right edge is dropped by
	// the buffer, taking the base with it, so keep the width that fits.
	if room := e.scr.Width() - e.openGrapheme.x; width > room {
		width = room
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

	// A continuation can change the cluster's width (a variation selector turns
	// a narrow base wide); move the cursor by the delta so following output
	// still lands after it.
	if width != e.openGrapheme.width {
		x, y := e.scr.CursorPosition()
		x += width - e.openGrapheme.width
		x = max(x, 0)
		if w := e.scr.Width(); x >= w {
			x = w - 1
			e.atPhantom = e.autoWrapMode()
		}
		e.scr.setCursor(x, y, false)
		e.openGrapheme.width = width
	}
}

// handleGrapheme handles UTF-8 graphemes.
func (e *Emulator) handleGrapheme(content string, width int) {
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
		x = 0
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
	if cell.Width > 1 && x+cell.Width > e.scr.Width() {
		e.scr.SetCell(x, y, nil)
		if !awm {
			// Nothing to wrap to. The column stays blank rather than holding
			// half a character.
			e.lastCellX, e.lastCellY = x, y
			e.scr.setCursor(x, y, false)
			return
		}
		e.index()
		_, y = e.scr.CursorPosition()
		x = 0
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
	e.scr.SetCell(x, y, &cell)

	// Pending wrap: the cursor stays on the character just drawn and the wrap
	// happens only when the next one arrives, so that a line ending exactly at
	// the margin does not scroll until there is something to put on the next
	// line. A wide cluster ending flush against the margin has to arm it too,
	// or the next character lands on that cluster's own second cell and eats
	// the character already there.
	if awm && cell.Width > 0 && x+cell.Width >= e.scr.Width() {
		e.atPhantom = true
		x = e.scr.Width() - 1
	} else {
		e.atPhantom = false
		x += cell.Width
	}

	// NOTE: We don't reset the phantom state here, we handle it up above.
	e.scr.setCursor(x, y, false)
}
