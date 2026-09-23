package vt

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// rgbSlot is one entry of Emulator.rgbCache. key is the colour's 24 bits
// with bit 24 set, so the zero slot matches nothing.
type rgbSlot struct {
	key uint32
	c   color.Color
}

// rgbColor returns the colour an SGR 38/48/58 with type 2 sets for r, g, b:
// the color.RGBA ansi.ReadStyleColor builds. Putting that value in a
// color.Color allocates, and a truecolor repaint sets a colour or two for
// every cell, so the boxed value is kept in a small direct-mapped cache and
// handed out again. The value is the same either way; only the allocation is
// shared.
func (e *Emulator) rgbColor(r, g, b uint8) color.Color {
	if e.rgbCache == nil {
		e.rgbCache = new([256]rgbSlot)
	}
	key := 1<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	slot := &e.rgbCache[(uint32(r)*7+uint32(g)*13+uint32(b)*31)&0xff]
	if slot.key != key {
		slot.key = key
		slot.c = color.RGBA{R: r, G: g, B: b, A: 0xff}
	}
	return slot.c
}

// rgbParams reports whether params starts with a direct RGB colour in one of
// the two shapes programs send, 38;2;r;g;b and 38:2:r:g:b (or 48, 58), and
// returns its components. These are exactly the shapes ansi.ReadStyleColor
// reads as five parameters with the colour in params[2:5]; any other shape,
// the colour-space forms included, is left to it.
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

// parseThemedColor parses an indexed or RGB color from SGR params, using theme colors for indices 0-15.
// It returns the color, the number of extra params consumed (to add to the
// loop index), and whether the params were read as a colour at all.
//
// A read can succeed with a nil colour: "38;0" is the implementation defined
// colour type, which ansi.ReadStyleColor consumes as two parameters and
// answers with no colour, so the pen goes back to its default. The caller has
// to skip those parameters anyway. Skipping only when the colour is non-nil
// read the 0 on as SGR 0 and reset the whole pen, where xterm, tmux and
// uv.ReadStyle all consume it as the colour type.
func (e *Emulator) parseThemedColor(params ansi.Params, i int) (color.Color, int, bool) {
	if r, g, b, ok := rgbParams(params[i:]); ok {
		return e.rgbColor(r, g, b), 4, true
	}
	// ansi.ReadStyleColor decides which shapes are a colour and how many
	// parameters each consumes. Deciding that here as well let a malformed
	// "38:5;7" read as colour 7, where ReadStyleColor, and ghostty, take the
	// separators disagreeing as no colour and read the 7 on as reverse.
	var c color.Color
	n := ansi.ReadStyleColor(params[i:], &c)
	if n == 0 {
		return nil, 0, false
	}
	// An indexed colour 0-15 (X;5;n) resolves through the theme.
	if _, indexed := c.(ansi.IndexedColor); indexed && n == 3 {
		if idx := params[i+2].Param(-1); idx >= 0 && idx <= 15 {
			c = e.IndexedColor(idx)
		}
	}
	return c, n - 1, true
}

// handleSgr handles Select Graphic Rendition (SGR) escape sequences.
//
// Every SGR goes through readStyleWithTheme, with or without a theme. The
// unthemed case used to go to uv.ReadStyle, which reads an underline
// subparameter it cannot name, such as "4:7", on as a bare SGR 7 and turns the
// cell reverse, and which drops SGR 21. The themed path already handled both,
// so a pane drew differently depending on whether a theme was set. The colours
// agree either way: with no palette slot claimed, PaletteColor and
// IndexedColor give the same plain palette entries uv.ReadStyle does.
func (e *Emulator) handleSgr(params ansi.Params) {
	// An SGR that is one truecolor colour and nothing else is what a
	// truecolor repaint sends for every cell. It is answered here without the
	// loop in readStyleWithTheme.
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

// readStyleWithTheme reads SGR sequences using our theme colors instead of hardcoded ANSI colors.
// This is based on uv.ReadStyle but uses IndexedColor to resolve theme colors.
func (e *Emulator) readStyleWithTheme(params ansi.Params, pen *uv.Style) {
	if len(params) == 0 {
		*pen = uv.Style{}
		return
	}

	for i := 0; i < len(params); i++ {
		param, hasMore, _ := params.Param(i, 0)
		switch param {
		case 0: // Reset
			*pen = uv.Style{}
		case 1: // Bold
			pen.Attrs |= uv.AttrBold
		case 2: // Dim/Faint
			pen.Attrs |= uv.AttrFaint
		case 3: // Italic
			pen.Attrs |= uv.AttrItalic
		case 4: // Underline
			nextParam, _, ok := params.Param(i+1, 0)
			if hasMore && ok {
				// A colon subparameter follows (e.g. 4:3). Always consume it,
				// even when the style value is out of range, so a stray value
				// like 4:7 is not reinterpreted as a separate SGR (7 reverse).
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
					// Unknown underline style: no-op, but still consumed above.
				}
			} else {
				pen.Underline = ansi.UnderlineSingle
			}
		case 5: // Slow Blink
			pen.Attrs |= uv.AttrBlink
		case 6: // Rapid Blink
			pen.Attrs |= uv.AttrRapidBlink
		case 7: // Reverse
			pen.Attrs |= uv.AttrReverse
		case 8: // Conceal
			pen.Attrs |= uv.AttrConceal
		case 9: // Crossed-out/Strikethrough
			pen.Attrs |= uv.AttrStrikethrough
		case 21: // Doubly underlined
			// ECMA-48 and xterm both use 21 for a double underline, and ghostty
			// and kitty follow them. Some older terminals used it for "bold
			// off", which is why 22 exists; uv.ReadStyle drops it entirely.
			pen.Underline = ansi.UnderlineDouble
		case 22: // Normal Intensity
			pen.Attrs &^= uv.AttrBold | uv.AttrFaint
		case 23: // Not italic
			pen.Attrs &^= uv.AttrItalic
		case 24: // Not underlined
			pen.Underline = ansi.UnderlineNone
		case 25: // Blink off
			pen.Attrs &^= uv.AttrBlink | uv.AttrRapidBlink
		case 27: // Positive (not reverse)
			pen.Attrs &^= uv.AttrReverse
		case 28: // Reveal
			pen.Attrs &^= uv.AttrConceal
		case 29: // Not crossed out
			pen.Attrs &^= uv.AttrStrikethrough
		case 30, 31, 32, 33, 34, 35, 36, 37: // Set foreground
			// PaletteColor, not IndexedColor: a slot no theme and no OSC 4 has
			// claimed stays SGR 3x on the way out, so the host paints it from
			// the user's own palette.
			pen.Fg = e.PaletteColor(int(param - 30))
		case 38: // Set foreground 256 or truecolor
			if c, skip, ok := e.parseThemedColor(params, i); ok {
				pen.Fg = c
				i += skip
			}
		case 39: // Default foreground
			pen.Fg = nil
		case 40, 41, 42, 43, 44, 45, 46, 47: // Set background
			pen.Bg = e.PaletteColor(int(param - 40))
		case 48: // Set background 256 or truecolor
			if c, skip, ok := e.parseThemedColor(params, i); ok {
				pen.Bg = c
				i += skip
			}
		case 49: // Default Background
			pen.Bg = nil
		case 58: // Set underline color
			if c, skip, ok := e.parseThemedColor(params, i); ok {
				pen.UnderlineColor = c
				i += skip
			}
		case 59: // Default underline color
			pen.UnderlineColor = nil
		case 90, 91, 92, 93, 94, 95, 96, 97: // Set bright foreground
			pen.Fg = e.PaletteColor(int(param - 90 + 8)) // 8-15 are bright colors
		case 100, 101, 102, 103, 104, 105, 106, 107: // Set bright background
			pen.Bg = e.PaletteColor(int(param - 100 + 8)) // 8-15 are bright colors
		default:
			// Delegate any scalar attribute code this switch does not
			// special-case to the canonical uv reader, so the themed path
			// stays attribute-complete with the non-themed path. Color codes
			// (38/48/58) and their subparameters are handled above, so this
			// only sees single scalar codes.
			uv.ReadStyle(params[i:i+1], pen)
		}
	}
}
