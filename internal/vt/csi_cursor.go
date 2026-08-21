package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// reportedCursorPosition returns the one-based line and column a cursor
// position report should carry, in that order. CPR is CSI Pl ; Pc R, line
// first.
//
// Under [ansi.DECOM] the numbers are relative to the scrolling region, because
// that is the coordinate system the guest is addressing the cursor in: a
// program that reads a report and feeds it straight back to CUP has to land
// where it started. xterm subtracts both margins here for the same reason
// (charproc.c, CASE_DSR).
func (e *Emulator) reportedCursorPosition() (line, col int) {
	x, y := e.scr.CursorPosition()
	if e.isModeSet(ansi.DECOM) {
		r := e.scr.ScrollRegion()
		x, y = x-r.Min.X, y-r.Min.Y
	}
	return y + 1, x + 1
}

// nextTab moves the cursor to the next tab stop n times. This respects the
// horizontal scrolling region. This performs the same function as [ansi.CHT].
func (e *Emulator) nextTab(n int) {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
	// n comes unclamped from a CSI param; a tab count beyond the column
	// count is meaningless once the cursor saturates at the edge.
	n = min(n, e.Width())
	for range n {
		ts := e.tabstops.Next(x)
		if ts < x {
			break
		}
		x = ts
	}

	if x >= scroll.Max.X {
		x = min(scroll.Max.X-1, e.Width()-1)
	}

	// NOTE: We use t.scr.setCursor here because we don't want to reset the
	// phantom state.
	e.scr.setCursor(x, y, false)
}

// prevTab moves the cursor to the previous tab stop n times. This respects the
// horizontal scrolling region when origin mode is set. If the cursor would
// move past the leftmost valid column, the cursor remains at the leftmost
// valid column and the operation completes.
func (e *Emulator) prevTab(n int) {
	x, _ := e.scr.CursorPosition()
	leftmargin := 0
	scroll := e.scr.ScrollRegion()
	if e.isModeSet(ansi.DECOM) {
		leftmargin = scroll.Min.X
	}

	// n comes unclamped from a CSI param; a tab count beyond the column
	// count is meaningless once the cursor saturates at the edge.
	n = min(n, e.Width())
	for range n {
		ts := e.tabstops.Prev(x)
		if ts > x {
			break
		}
		x = ts
	}

	if x < leftmargin {
		x = leftmargin
	}

	// NOTE: We use t.scr.setCursorX here because we don't want to reset the
	// phantom state.
	e.scr.setCursorX(x, false)
}

// moveCursor moves the cursor by the given x and y deltas. If the cursor
// is at phantom, the state will reset and the cursor is back in the screen.
func (e *Emulator) moveCursor(dx, dy int) {
	e.scr.moveCursor(dx, dy)
	e.atPhantom = false
}

// setCursor sets the cursor position. This resets the phantom state.
func (e *Emulator) setCursor(x, y int) {
	e.scr.setCursor(x, y, false)
	e.atPhantom = false
}

// setCursorPosition sets the cursor position. This respects [ansi.DECOM],
// Origin Mode. This performs the same function as [ansi.CUP].
func (e *Emulator) setCursorPosition(x, y int) {
	margins := e.isModeSet(ansi.DECOM)
	e.scr.setCursor(x, y, margins)
	e.atPhantom = false
}

// carriageReturn moves the cursor to the leftmost column. If [ansi.DECOM] is
// set, the cursor is set to the left margin. If not, and the cursor is on or
// to the right of the left margin, the cursor is set to the left margin.
// Otherwise, the cursor is set to the leftmost column of the screen.
// This performs the same function as [ansi.CR].
func (e *Emulator) carriageReturn() {
	margins := e.isModeSet(ansi.DECOM)
	x, y := e.scr.CursorPosition()
	if margins {
		// y is the current absolute row; keep it absolute and only move X to
		// the left margin. Passing margins=true here would re-add scroll.Min.Y
		// to an already-absolute y and jump the cursor down by the top margin.
		region := e.scr.ScrollRegion()
		e.scr.setCursor(region.Min.X, y, false)
	} else if region := e.scr.ScrollRegion(); uv.Pos(x, y).In(region) {
		e.scr.setCursor(region.Min.X, y, false)
	} else {
		e.scr.setCursor(0, y, false)
	}
	e.atPhantom = false
}

// repeatPreviousCharacter repeats the previous character n times. This is
// equivalent to typing the same character n times. This performs the same as
// [ansi.REP].
func (e *Emulator) repeatPreviousCharacter(n int) {
	if e.lastCluster == "" {
		return
	}
	// n comes unclamped from a CSI param (up to ~2.1 billion); repeating
	// past a full screen only fills and scrolls pointlessly, so cap it to
	// one screenful of cells. Guard against a zero-sized screen.
	if maxN := e.Width() * e.Height(); maxN > 0 && n > maxN {
		n = maxN
	}
	// Held in a local because handleGrapheme writes them back on every call,
	// which is harmless while they stay the same and would not survive a
	// future print path that rewrote the cluster.
	cluster, width := e.lastCluster, e.lastClusterWidth
	for range n {
		e.handleGrapheme(cluster, width)
	}
}
