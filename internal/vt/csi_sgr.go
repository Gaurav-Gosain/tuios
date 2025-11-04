package vt

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

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
			*pen = pen.Bold(true)
		case 2: // Dim/Faint
			*pen = pen.Faint(true)
		case 3: // Italic
			*pen = pen.Italic(true)
		case 4: // Underline
			nextParam, _, ok := params.Param(i+1, 0)
			if hasMore && ok {
				switch nextParam {
				case 0, 1, 2, 3, 4, 5:
					i++
					switch nextParam {
					case 0:
						*pen = pen.UnderlineStyle(uv.NoUnderline)
					case 1:
						*pen = pen.UnderlineStyle(uv.SingleUnderline)
					case 2:
						*pen = pen.UnderlineStyle(uv.DoubleUnderline)
					case 3:
						*pen = pen.UnderlineStyle(uv.CurlyUnderline)
					case 4:
						*pen = pen.UnderlineStyle(uv.DottedUnderline)
					case 5:
						*pen = pen.UnderlineStyle(uv.DashedUnderline)
					}
				}
			} else {
				*pen = pen.UnderlineStyle(uv.SingleUnderline)
			}
		case 5: // Slow Blink
			*pen = pen.SlowBlink(true)
		case 6: // Rapid Blink
			*pen = pen.RapidBlink(true)
		case 7: // Reverse
			*pen = pen.Reverse(true)
		case 8: // Conceal
			*pen = pen.Conceal(true)
		case 9: // Crossed-out/Strikethrough
			*pen = pen.Strikethrough(true)
		case 22: // Normal Intensity
			*pen = pen.Bold(false).Faint(false)
		case 23: // Not italic
			*pen = pen.Italic(false)
		case 24: // Not underlined
			*pen = pen.UnderlineStyle(uv.NoUnderline)
		case 25: // Blink off
			*pen = pen.SlowBlink(false).RapidBlink(false)
		case 27: // Positive (not reverse)
			*pen = pen.Reverse(false)
		case 28: // Reveal
			*pen = pen.Conceal(false)
		case 29: // Not crossed out
			*pen = pen.Strikethrough(false)
		case 30, 31, 32, 33, 34, 35, 36, 37: // Set foreground - USE THEME COLORS
			*pen = pen.Foreground(e.IndexedColor(int(param - 30)))
		case 38: // Set foreground 256 or truecolor
			// Check if this is indexed color format (38;5;n) and if n is 0-15
			if i+2 < len(params) {
				next, _, _ := params.Param(i+1, -1)
				if next == 5 {
					// This is indexed color format
					colorIndex, _, _ := params.Param(i+2, -1)
					if colorIndex >= 0 && colorIndex <= 15 {
						// Use our themed color for indices 0-15
						*pen = pen.Foreground(e.IndexedColor(int(colorIndex)))
						i += 2 // Skip the 5 and color index parameters
						continue
					}
				}
			}
			// For all other cases (indices 16-255, RGB colors, etc), use standard reading
			var c color.Color
			n := ansi.ReadStyleColor(params[i:], &c)
			if n > 0 {
				*pen = pen.Foreground(c)
				i += n - 1
			}
		case 39: // Default foreground
			*pen = pen.Foreground(e.defaultFg)
		case 40, 41, 42, 43, 44, 45, 46, 47: // Set background - USE THEME COLORS
			*pen = pen.Background(e.IndexedColor(int(param - 40)))
		case 48: // Set background 256 or truecolor
			// Check if this is indexed color format (48;5;n) and if n is 0-15
			if i+2 < len(params) {
				next, _, _ := params.Param(i+1, -1)
				if next == 5 {
					// This is indexed color format
					colorIndex, _, _ := params.Param(i+2, -1)
					if colorIndex >= 0 && colorIndex <= 15 {
						// Use our themed color for indices 0-15
						*pen = pen.Background(e.IndexedColor(int(colorIndex)))
						i += 2 // Skip the 5 and color index parameters
						continue
					}
				}
			}
			// For all other cases (indices 16-255, RGB colors, etc), use standard reading
			var c color.Color
			n := ansi.ReadStyleColor(params[i:], &c)
			if n > 0 {
				*pen = pen.Background(c)
				i += n - 1
			}
		case 49: // Default Background
			*pen = pen.Background(e.defaultBg)
		case 58: // Set underline color
			// Check if this is indexed color format (58;5;n) and if n is 0-15
			if i+2 < len(params) {
				next, _, _ := params.Param(i+1, -1)
				if next == 5 {
					// This is indexed color format
					colorIndex, _, _ := params.Param(i+2, -1)
					if colorIndex >= 0 && colorIndex <= 15 {
						// Use our themed color for indices 0-15
						*pen = pen.Underline(e.IndexedColor(int(colorIndex)))
						i += 2 // Skip the 5 and color index parameters
						continue
					}
				}
			}
			// For all other cases (indices 16-255, RGB colors, etc), use standard reading
			var c color.Color
			n := ansi.ReadStyleColor(params[i:], &c)
			if n > 0 {
				*pen = pen.Underline(c)
				i += n - 1
			}
		case 59: // Default underline color
			*pen = pen.Underline(nil)
		case 90, 91, 92, 93, 94, 95, 96, 97: // Set bright foreground - USE THEME COLORS
			*pen = pen.Foreground(e.IndexedColor(int(param - 90 + 8))) // 8-15 are bright colors
		case 100, 101, 102, 103, 104, 105, 106, 107: // Set bright background - USE THEME COLORS
			*pen = pen.Background(e.IndexedColor(int(param - 100 + 8))) // 8-15 are bright colors
		}
	}
}
