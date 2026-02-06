package vt

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// parseThemedColor parses an indexed or RGB color from SGR params, using theme colors for indices 0-15.
// Returns the color and the number of extra params consumed (to add to loop index).
func (e *Emulator) parseThemedColor(params ansi.Params, i int) (color.Color, int) {
	// Check if this is indexed color format (X;5;n) and if n is 0-15
	if i+2 < len(params) {
		next, _, _ := params.Param(i+1, -1)
		if next == 5 {
			colorIndex, _, _ := params.Param(i+2, -1)
			if colorIndex >= 0 && colorIndex <= 15 {
				return e.IndexedColor(colorIndex), 2
			}
		}
	}
	// For all other cases (indices 16-255, RGB colors, etc), use standard reading
	var c color.Color
	n := ansi.ReadStyleColor(params[i:], &c)
	if n > 0 {
		return c, n - 1
	}
	return nil, 0
}

// handleSgr handles SGR escape sequences.
// handleSgr handles Select Graphic Rendition (SGR) escape sequences.
func (e *Emulator) handleSgr(params ansi.Params) {
	// If theming is disabled or no theme colors are set, use standard ultraviolet handling
	if !e.hasThemeColors() {
		uv.ReadStyle(params, &e.scr.cur.Pen)
		return
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
				switch nextParam {
				case 0, 1, 2, 3, 4, 5:
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
					}
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
		case 30, 31, 32, 33, 34, 35, 36, 37: // Set foreground - USE THEME COLORS
			pen.Fg = e.IndexedColor(int(param - 30))
		case 38: // Set foreground 256 or truecolor
			if c, skip := e.parseThemedColor(params, i); c != nil {
				pen.Fg = c
				i += skip
			}
		case 39: // Default foreground
			pen.Fg = nil
		case 40, 41, 42, 43, 44, 45, 46, 47: // Set background - USE THEME COLORS
			pen.Bg = e.IndexedColor(int(param - 40))
		case 48: // Set background 256 or truecolor
			if c, skip := e.parseThemedColor(params, i); c != nil {
				pen.Bg = c
				i += skip
			}
		case 49: // Default Background
			pen.Bg = nil
		case 58: // Set underline color
			if c, skip := e.parseThemedColor(params, i); c != nil {
				pen.UnderlineColor = c
				i += skip
			}
		case 59: // Default underline color
			pen.UnderlineColor = nil
		case 90, 91, 92, 93, 94, 95, 96, 97: // Set bright foreground - USE THEME COLORS
			pen.Fg = e.IndexedColor(int(param - 90 + 8)) // 8-15 are bright colors
		case 100, 101, 102, 103, 104, 105, 106, 107: // Set bright background - USE THEME COLORS
			pen.Bg = e.IndexedColor(int(param - 100 + 8)) // 8-15 are bright colors
		}
	}
}
