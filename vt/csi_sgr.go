package vt

import (
	"image/color"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type rgbSlot struct {
	key uint32
	c   color.Color
}

const rgbSlabLen = 64

func (e *Emulator) rgbColor(r, g, b uint8) color.Color {
	if e.rgbCache == nil {
		e.rgbCache = new([256]rgbSlot)
	}
	key := 1<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	slot := &e.rgbCache[(uint32(r)*7+uint32(g)*13+uint32(b)*31)&0xff]
	if slot.key != key {
		slot.key = key
		slot.c = e.boxRGB(color.RGBA{R: r, G: g, B: b, A: 0xff})
	}
	return slot.c
}

var rgbaType color.Color = color.RGBA{}

type ifaceWords struct {
	tab  unsafe.Pointer
	data unsafe.Pointer
}

func (e *Emulator) boxRGB(c color.RGBA) color.Color {
	if len(e.rgbSlab) == 0 {
		e.rgbSlab = make([]color.RGBA, rgbSlabLen)
	}
	v := &e.rgbSlab[0]
	*v = c
	e.rgbSlab = e.rgbSlab[1:]
	out := rgbaType
	// #nosec G103 - replaces only the value pointer of an interface whose
	(*ifaceWords)(unsafe.Pointer(&out)).data = unsafe.Pointer(v)
	return out
}

func rgbParams(params ansi.Params) (r, g, b uint8, ok bool) {
	if len(params) < 5 || params[1].Param(0) != 2 || params[4].HasMore() {
		return 0, 0, 0, false
	}
	colon := params[0].HasMore()
	for _, p := range params[1:4] {
		if p.HasMore() != colon {
			return 0, 0, 0, false
		}
	}
	return uint8(params[2].Param(0)), uint8(params[3].Param(0)), uint8(params[4].Param(0)), true //nolint:gosec
}

func (e *Emulator) parseThemedColor(params ansi.Params, i int) (color.Color, int, bool) {
	if r, g, b, ok := rgbParams(params[i:]); ok {
		return e.rgbColor(r, g, b), 4, true
	}
	var c color.Color
	n := ansi.ReadStyleColor(params[i:], &c)
	if n == 0 {
		return nil, 0, false
	}
	if _, indexed := c.(ansi.IndexedColor); indexed && n == 3 {
		if idx := params[i+2].Param(-1); idx >= 0 && idx <= 15 {
			c = e.IndexedColor(idx)
		}
	}
	return c, n - 1, true
}

func (e *Emulator) handleSgr(params ansi.Params) {
	if len(params) == 5 {
		if r, g, b, ok := rgbParams(params); ok {
			switch params[0].Param(0) {
			case 38:
				e.scr.cur.Pen.Fg = e.rgbColor(r, g, b)
				return
			case 48:
				e.scr.cur.Pen.Bg = e.rgbColor(r, g, b)
				return
			case 58:
				e.scr.cur.Pen.UnderlineColor = e.rgbColor(r, g, b)
				return
			}
		}
	}

	e.readStyleWithTheme(params, &e.scr.cur.Pen)
}

func (e *Emulator) readStyleWithTheme(params ansi.Params, pen *uv.Style) {
	if len(params) == 0 {
		*pen = uv.Style{}
		return
	}

	for i := 0; i < len(params); i++ {
		param, hasMore, _ := params.Param(i, 0)
		switch param {
		case 0:
			*pen = uv.Style{}
		case 1:
			pen.Attrs |= uv.AttrBold
		case 2:
			pen.Attrs |= uv.AttrFaint
		case 3:
			pen.Attrs |= uv.AttrItalic
		case 4:
			nextParam, _, ok := params.Param(i+1, 0)
			if hasMore && ok {
				i++
				switch nextParam {
				case 0:
					pen.Underline = ansi.UnderlineNone
				case 1:
					pen.Underline = ansi.UnderlineSingle
				case 2:
					pen.Underline = ansi.UnderlineDouble
				case 3:
					pen.Underline = ansi.UnderlineCurly
				case 4:
					pen.Underline = ansi.UnderlineDotted
				case 5:
					pen.Underline = ansi.UnderlineDashed
				default:
				}
			} else {
				pen.Underline = ansi.UnderlineSingle
			}
		case 5:
			pen.Attrs |= uv.AttrBlink
		case 6:
			pen.Attrs |= uv.AttrRapidBlink
		case 7:
			pen.Attrs |= uv.AttrReverse
		case 8:
			pen.Attrs |= uv.AttrConceal
		case 9:
			pen.Attrs |= uv.AttrStrikethrough
		case 21:
			pen.Underline = ansi.UnderlineDouble
		case 22:
			pen.Attrs &^= uv.AttrBold | uv.AttrFaint
		case 23:
			pen.Attrs &^= uv.AttrItalic
		case 24:
			pen.Underline = ansi.UnderlineNone
		case 25:
			pen.Attrs &^= uv.AttrBlink | uv.AttrRapidBlink
		case 27:
			pen.Attrs &^= uv.AttrReverse
		case 28:
			pen.Attrs &^= uv.AttrConceal
		case 29:
			pen.Attrs &^= uv.AttrStrikethrough
		case 30, 31, 32, 33, 34, 35, 36, 37:
			pen.Fg = e.PaletteColor(int(param - 30))
		case 38:
			if c, skip, ok := e.parseThemedColor(params, i); ok {
				pen.Fg = c
				i += skip
			}
		case 39:
			pen.Fg = nil
		case 40, 41, 42, 43, 44, 45, 46, 47:
			pen.Bg = e.PaletteColor(int(param - 40))
		case 48:
			if c, skip, ok := e.parseThemedColor(params, i); ok {
				pen.Bg = c
				i += skip
			}
		case 49:
			pen.Bg = nil
		case 58:
			if c, skip, ok := e.parseThemedColor(params, i); ok {
				pen.UnderlineColor = c
				i += skip
			}
		case 59:
			pen.UnderlineColor = nil
		case 90, 91, 92, 93, 94, 95, 96, 97:
			pen.Fg = e.PaletteColor(int(param - 90 + 8))
		case 100, 101, 102, 103, 104, 105, 106, 107:
			pen.Bg = e.PaletteColor(int(param - 100 + 8))
		case 53, 55:
			e.logf("unhandled sequence: SGR %d", param)
		default:
			uv.ReadStyle(params[i:i+1], pen)
		}
	}
}
