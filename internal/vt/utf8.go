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

// asciiStr holds the 128 single-byte ASCII strings so the printable-ASCII fast
// path in handlePrint can pass a package-lifetime string to handleGrapheme
// instead of allocating string(r) (which escapes to the heap) for every char.
var asciiStr [128]string

func init() {
	for i := range asciiStr {
		asciiStr[i] = string(rune(i))
	}
}

// openGrapheme records a cluster that was drawn at the end of a Write while
// more of it may still be in flight, along with the cell it landed in.
type openGrapheme struct {
	active bool
	x, y   int
	width  int
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
	} else {
		e.grapheme = append(e.grapheme, r)
		if e.openGrapheme.active {
			e.extendOpenGrapheme()
		}
	}
}

// flushGrapheme flushes the current grapheme buffer, if any, and handles the
// grapheme as a single unit.
func (e *Emulator) flushGrapheme() {
	if len(e.grapheme) == 0 {
		return
	}
	// An open cluster is already on screen; the arriving sequence closes it,
	// so retire the buffer instead of drawing it a second time.
	if e.openGrapheme.active {
		e.openGrapheme.active = false
		e.grapheme = e.grapheme[:0]
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
			e.openGrapheme = openGrapheme{
				active: true,
				x:      e.lastCellX,
				y:      e.lastCellY,
				width:  width,
			}
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
		e.openGrapheme.active = false
		e.grapheme = append(e.grapheme[:0], []rune(s[len(cluster):])...)
		return
	}

	cell := uv.Cell{
		Content: cluster,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}
	e.scr.SetCell(e.openGrapheme.x, e.openGrapheme.y, &cell)

	// A continuation can change the cluster's width (a variation selector turns
	// a narrow base wide); move the cursor by the delta so following output
	// still lands after it.
	if width != e.openGrapheme.width {
		x, y := e.scr.CursorPosition()
		x += width - e.openGrapheme.width
		x = max(x, 0)
		if w := e.scr.Width(); x >= w {
			x = w - 1
			e.atPhantom = e.isModeSet(ansi.ModeAutoWrap)
		}
		e.scr.setCursor(x, y, false)
		e.openGrapheme.width = width
	}
}

// handleGrapheme handles UTF-8 graphemes.
func (e *Emulator) handleGrapheme(content string, width int) {
	awm := e.isModeSet(ansi.ModeAutoWrap)
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

	if cell.Width == 1 && len(content) == 1 {
		e.lastChar, _ = utf8.DecodeRuneInString(content)
	}

	e.lastCellX, e.lastCellY = x, y
	e.scr.SetCell(x, y, &cell)

	// Handle phantom state at the end of the line
	e.atPhantom = awm && x >= e.scr.Width()-1
	if !e.atPhantom {
		x += cell.Width
	}

	// NOTE: We don't reset the phantom state here, we handle it up above.
	e.scr.setCursor(x, y, false)
}
