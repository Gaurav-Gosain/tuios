package session

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"tuios.dev/shot"
)

var xtermDefault = func() [16]color.Color {
	var pal [16]color.Color
	for i, c := range shot.XTermPalette().ANSI {
		pal[i] = c
	}
	return pal
}()

func xtermPalette() [16]color.Color {
	return xtermDefault
}

func paletteFromParams(hex []string) ([16]color.Color, *verbError) {
	if len(hex) == 0 {
		return xtermPalette(), nil
	}
	if len(hex) != 16 {
		return [16]color.Color{}, newVerbError(ErrVerbInvalidParams, "palette must have exactly 16 hex colours (#rrggbb), got "+strconv.Itoa(len(hex)))
	}
	var pal [16]color.Color
	for i, h := range hex {
		c, ok := overlay.ParseHex(h)
		if !ok {
			return [16]color.Color{}, newVerbError(ErrVerbInvalidParams, "palette["+strconv.Itoa(i)+"] is not a hex colour: "+h)
		}
		pal[i] = c
	}
	return pal, nil
}

func rgbOf(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

func csiFieldInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func sgrUnits(params []string) [][]string {
	var units [][]string
	for i := 0; i < len(params); i++ {
		n, ok := csiFieldInt(params[i])
		if !ok {
			units = append(units, []string{params[i]})
			continue
		}
		if n == 38 || n == 48 {
			if i+2 < len(params) {
				mid, midOK := csiFieldInt(params[i+1])
				switch {
				case midOK && mid == 5:
					if _, last := csiFieldInt(params[i+2]); last {
						units = append(units, []string{params[i], params[i+1], params[i+2]})
						i += 2
						continue
					}
				case midOK && mid == 2 && i+4 < len(params):
					if _, r := csiFieldInt(params[i+2]); r {
						if _, g := csiFieldInt(params[i+3]); g {
							if _, b := csiFieldInt(params[i+4]); b {
								units = append(units, []string{params[i], params[i+1], params[i+2], params[i+3], params[i+4]})
								i += 4
								continue
							}
						}
					}
				}
			}
		}
		units = append(units, []string{params[i]})
	}
	return units
}

func resolveSGRUnit(u []string, pal [16]color.Color) []string {
	n, ok := csiFieldInt(u[0])
	if !ok {
		return u
	}
	if len(u) == 1 {
		switch {
		case n >= 30 && n <= 37:
			return trueColourUnit(38, pal[n-30])
		case n >= 40 && n <= 47:
			return trueColourUnit(48, pal[n-40])
		case n >= 90 && n <= 97:
			return trueColourUnit(38, pal[n-90+8])
		case n >= 100 && n <= 107:
			return trueColourUnit(48, pal[n-100+8])
		}
		return u
	}
	if (n == 38 || n == 48) && u[1] == "5" {
		idx := mustIntOrZero(u[2])
		switch {
		case idx < 0 || idx > 255:
			return u
		case idx < 16:
			return trueColourUnit(n, pal[idx])
		default:
			c := shot.XTerm256(idx)
			return rgbUnit(n, c.R, c.G, c.B)
		}
	}
	return u
}

func mustIntOrZero(s string) int {
	n, _ := csiFieldInt(s)
	return n
}

func rgbUnit(prefix int, r, g, b uint8) []string {
	return []string{
		strconv.Itoa(prefix), "2",
		strconv.Itoa(int(r)), strconv.Itoa(int(g)), strconv.Itoa(int(b)),
	}
}

func trueColourUnit(prefix int, c color.Color) []string {
	r, g, b := rgbOf(c)
	return rgbUnit(prefix, r, g, b)
}

func resolveSGRParams(params []string, pal [16]color.Color) []string {
	units := sgrUnits(params)
	out := make([]string, 0, len(params)+4)
	for _, u := range units {
		out = append(out, resolveSGRUnit(u, pal)...)
	}
	return out
}

func parseCSIParams(s string) []string {
	return strings.Split(s, ";")
}

func joinCSIParams(params []string) string {
	return strings.Join(params, ";")
}

// ResolveSGR rewrites index colours in an ANSI-styled capture to 24-bit RGB
// against the given palette. It walks the string looking for CSI SGR
// sequences ("\x1b[...m") and transforms their colour parameters; every other
// byte, and every non-SGR CSI sequence, passes through untouched. It is pure
// and backend-agnostic: it runs on the output of either emulator's Render.
func ResolveSGR(content string, palette [16]color.Color) string {
	if !strings.Contains(content, "\x1b[") {
		return content
	}
	var b strings.Builder
	b.Grow(len(content) + 32)
	for i := 0; i < len(content); {
		if content[i] != 0x1b || i+1 >= len(content) || content[i+1] != '[' {
			b.WriteByte(content[i])
			i++
			continue
		}
		j := i + 2
		for j < len(content) && !(content[j] >= 0x40 && content[j] <= 0x7e) {
			j++
		}
		if j >= len(content) {
			b.WriteString(content[i:])
			break
		}
		if content[j] == 'm' {
			params := parseCSIParams(content[i+2 : j])
			resolved := resolveSGRParams(params, palette)
			if slices.Equal(resolved, params) {
				b.WriteString(content[i : j+1])
			} else {
				b.WriteString("\x1b[")
				b.WriteString(joinCSIParams(resolved))
				b.WriteByte('m')
			}
		} else {
			b.WriteString(content[i : j+1])
		}
		i = j + 1
	}
	return b.String()
}
