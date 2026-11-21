package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

func (e *Emulator) reportedCursorPosition() (line, col int) {
	x, y := e.scr.CursorPosition()
	if e.originMode() {
		r := e.scr.ScrollRegion()
		x, y = x-r.Min.X, y-r.Min.Y
	}
	return y + 1, x + 1
}

func (e *Emulator) nextTab(n int) {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
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

	e.scr.setCursor(x, y, false)
}

func (e *Emulator) prevTab(n int) {
	x, _ := e.scr.CursorPosition()
	leftmargin := 0
	scroll := e.scr.ScrollRegion()
	if e.originMode() {
		leftmargin = scroll.Min.X
	}

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

	e.scr.setCursorX(x, false)
}

func (e *Emulator) moveCursor(dx, dy int) {
	e.scr.moveCursor(dx, dy)
	e.atPhantom = false
}

func (e *Emulator) setCursor(x, y int) {
	e.scr.setCursor(x, y, false)
	e.atPhantom = false
}

func (e *Emulator) setCursorPosition(x, y int) {
	margins := e.originMode()
	e.scr.setCursor(x, y, margins)
	e.atPhantom = false
}

func (e *Emulator) carriageReturn() {
	margins := e.originMode()
	x, y := e.scr.CursorPosition()
	if margins {
		region := e.scr.ScrollRegion()
		e.scr.setCursor(region.Min.X, y, false)
	} else if region := e.scr.ScrollRegion(); uv.Pos(x, y).In(region) {
		e.scr.setCursor(region.Min.X, y, false)
	} else {
		e.scr.setCursor(0, y, false)
	}
	e.atPhantom = false
}

func (e *Emulator) repeatPreviousCharacter(n int) {
	if e.lastCluster == "" {
		return
	}
	if maxN := e.Width() * e.Height(); maxN > 0 && n > maxN {
		n = maxN
	}
	cluster := e.lastCluster
	for range n {
		for _, r := range cluster {
			e.handlePrint(r)
		}
	}
}
