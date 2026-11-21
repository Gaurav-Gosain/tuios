package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) eraseCharacter(n int) {
	if n <= 0 {
		n = 1
	}
	x, y := e.scr.CursorPosition()
	if rem := e.scr.Width() - x; n > rem {
		n = rem
	}
	if n <= 0 {
		e.atPhantom = false
		return
	}
	rect := uv.Rect(x, y, n, 1)
	e.scr.FillArea(e.scr.blankCell(), rect)
	e.scr.buf.setSoftWrapped(y, false)
	e.atPhantom = false
}

func (e *Emulator) markPrinted(x, y, n int) {
	if e.scr.cur.Protected {
		e.scr.buf.setProtected(x, y, max(n, 1), true)
	}
}

func (e *Emulator) eraseFill(selective bool) func(c *uv.Cell, area uv.Rectangle) {
	if !selective {
		return func(c *uv.Cell, area uv.Rectangle) { e.scr.FillArea(c, area) }
	}
	return e.fillUnprotected
}

func (e *Emulator) fillUnprotected(c *uv.Cell, area uv.Rectangle) {
	buf := e.scr.buf
	if buf.prot == nil {
		e.scr.FillArea(c, area)
		return
	}
	area = area.Intersect(buf.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; {
			if buf.Protected(x, y) {
				x++
				continue
			}
			start := x
			for x < area.Max.X && !buf.Protected(x, y) {
				x++
			}
			e.scr.FillArea(c, uv.Rect(start, y, x-start, 1))
		}
	}
}

func (e *Emulator) eraseDisplay(params ansi.Params, selective bool, fill func(*uv.Cell, uv.Rectangle)) bool {
	n, _, _ := params.Param(0, 0)
	width, height := e.Width(), e.Height()
	x, y := e.scr.CursorPosition()
	switch n {
	case 0:
		rect1 := uv.Rect(x, y, width, 1)
		rect2 := uv.Rect(0, y+1, width, height-y-1)
		fill(e.scr.blankCell(), rect1)
		fill(e.scr.blankCell(), rect2)
		if x == 0 && y == 0 && e.cb.ScreenClear != nil {
			e.cb.ScreenClear()
		}
	case 1:
		if y > 0 {
			fill(e.scr.blankCell(), uv.Rect(0, 0, width, y))
		}
		fill(e.scr.blankCell(), uv.Rect(0, y, min(x+1, width), 1))
	case 2:
		if selective {
			fill(nil, e.scr.Bounds())
		} else {
			fill(e.scr.blankCell(), e.scr.Bounds())
		}
		e.KittyState().ClearPlacements()
		if e.semanticMarkers != nil {
			e.semanticMarkers.RemoveOnScreen(e.ScrollbackLen())
		}
		if e.cb.ScreenClear != nil {
			e.cb.ScreenClear()
		}
	case 3:
		e.scr.ClearScrollback()
	default:
		return false
	}
	return true
}

func (e *Emulator) eraseLine(params ansi.Params, selective bool, fill func(*uv.Cell, uv.Rectangle)) bool {
	n, _, _ := params.Param(0, 0)
	x, y := e.scr.CursorPosition()
	w := e.scr.Width()

	switch n {
	case 0:
		if !selective {
			e.eraseCharacter(w - x)
			break
		}
		fill(e.scr.blankCell(), uv.Rect(x, y, w-x, 1))
		e.scr.buf.setSoftWrapped(y, false)
		e.atPhantom = false
	case 1:
		fill(e.scr.blankCell(), uv.Rect(0, y, x+1, 1))
	case 2:
		fill(e.scr.blankCell(), uv.Rect(0, y, w, 1))
	default:
		return false
	}
	return true
}
