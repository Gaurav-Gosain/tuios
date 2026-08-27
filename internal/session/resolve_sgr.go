package session

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
)

// This file implements resolved colour capture for capture-pane.
//
// The daemon-side emulator stores colours exactly as the guest sent them: a
// program that emits SGR 31 leaves an ansi.BasicColor(1) in the cell, and
// Render() re-emits 31. That is the intended contract — appearance is
// client-owned, and a consumer of capture-pane resolves indices against its
// own palette. But a client that wants to pipe a capture into a tool which
// renders verbatim (no palette of its own) has no way to get the colours it
// actually paints. ResolveSGR is that way: it rewrites SGR index colours,
// including the standard 256-colour cube and grey ramp, to 24-bit RGB. Indices
// 0-15 resolve against the palette the caller supplies, which is the client's
// theme palette; everything else has a fixed value no consumer redefines. The
// daemon still knows nothing about themes; the palette is an explicit
// parameter of the request.

// xtermDefaultHex is xterm's own default 16-colour palette, as hex literals.
//
// This deliberately is not derived from a colour library's BasicColor, whose
// indices follow the darker VGA scheme (index 1 maroon, index 4 navy). A
// capture resolved against those shades disagrees with what xterm actually
// paints when a client sends no palette of its own, and the default has to be
// the honest one.
var xtermDefaultHex = [16]string{
	"#000000", "#cd0000", "#00cd00", "#cdcd00",
	"#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
	"#7f7f7f", "#ff0000", "#00ff00", "#ffff00",
	"#5c5cff", "#ff00ff", "#00ffff", "#ffffff",
}

// xtermPalette returns the standard xterm 16-colour palette as RGB, used when
// a resolved capture arrives without an explicit palette. The daemon has no
// theme, so this is the honest default: better than emitting indices for a
// consumer that cannot resolve them, and identical for every caller.
func xtermPalette() [16]color.Color {
	var pal [16]color.Color
	for i, h := range xtermDefaultHex {
		c, ok := parseHexColor(h)
		if !ok {
			panic("tuios: built-in xterm colour " + h + " is not hex")
		}
		pal[i] = c
	}
	return pal
}

// parseHexColor reads #rgb or #rrggbb, with or without the leading hash. It is
// the session-package twin of the app's parseHexColor: the daemon must accept
// a palette without importing the client package.
func parseHexColor(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return color.RGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}, true
}

// paletteFromParams builds a 16-colour palette from the hex strings a client
// sent with a resolved capture. An empty slice means "no palette given" and
// falls back to the xterm default. Any other length, or an unparsable entry,
// is an error so a client that meant to theme the capture finds out its
// palette was wrong instead of silently getting xterm colours.
func paletteFromParams(hex []string) ([16]color.Color, *verbError) {
	if len(hex) == 0 {
		return xtermPalette(), nil
	}
	if len(hex) != 16 {
		return [16]color.Color{}, newVerbError(ErrVerbInvalidParams, "palette must have exactly 16 hex colours (#rrggbb), got "+strconv.Itoa(len(hex)))
	}
	var pal [16]color.Color
	for i, h := range hex {
		c, ok := parseHexColor(h)
		if !ok {
			return [16]color.Color{}, newVerbError(ErrVerbInvalidParams, "palette["+strconv.Itoa(i)+"] is not a hex colour: "+h)
		}
		pal[i] = c
	}
	return pal, nil
}

// rgbOf extracts the 8-bit RGB channels of a palette colour. The palette is
// always RGB (hex colours from the client, or the xterm table built above),
// so the shift is lossless; it is written generically because the palette
// entries arrive as color.Color.
func rgbOf(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// csiFieldInt parses one CSI parameter field that is expected to be a plain
// integer. Anything else fails: the empty field (which SGR reads as the
// default, usually 0) and colon-coded sub-parameters such as "4:3" are not
// integers and must never be guessed into becoming one, because SGR 0 is the
// total reset and flattening "4:3" into it erases every attribute set before
// it.
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

// xterm256ToRGB resolves a standard 256-colour index to its fixed 24-bit RGB
// value. These colours are not part of any palette a theme can override: the
// 6x6x6 cube (16-231) and the grey ramp (232-255) carry their levels in the
// index itself, so every terminal resolves them identically.
func xterm256ToRGB(n int) (uint8, uint8, uint8) {
	switch {
	case n >= 232:
		grey := uint8(8 + 10*(n-232))
		return grey, grey, grey
	default: // 16 <= n <= 231
		n -= 16
		return colourCubeLevel(n / 36), colourCubeLevel((n / 6) % 6), colourCubeLevel(n % 6)
	}
}

// colourCubeLevel converts one cube coordinate (0-5) into an 8-bit channel:
// zero levels stay dark and the rest spread 55 to 235 evenly.
func colourCubeLevel(v int) uint8 {
	if v == 0 {
		return 0
	}
	return uint8(55 + 40*v)
}

// sgrUnits splits an SGR parameter list into units, where a unit is one
// parameter together with any it extends: 38;5;n and 38;2;r;g;b each form a
// single unit, as do 48;5;n and 48;2;r;g;b. Everything else is a unit of one.
//
// Fields travel as their original text and are only turned into numbers when
// they really are plain integers. A field that carries a colon ("4:3") or is
// empty therefore stands alone, verbatim, instead of being folded into some
// neighbouring colour or reset; and a colour introducer only swallows its
// continuation when every continuation field parses, so malformed input is
// passed through rather than misread.
func sgrUnits(params []string) [][]string {
	var units [][]string
	for i := 0; i < len(params); i++ {
		n, ok := csiFieldInt(params[i])
		if !ok {
			units = append(units, []string{params[i]})
			continue
		}
		if n == 38 || n == 48 {
			// Colour: either 38;5;n (indexed) or 38;2;r;g;b (true colour),
			// consumed only when the continuation fields are integers too.
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

// resolveSGRUnit rewrites one SGR unit to 24-bit RGB when it is an index
// colour, and returns it unchanged otherwise. Units whose head field is not a
// plain integer are copied verbatim: they were split off precisely because
// guessing at them could turn a harmless sub-parameter into a reset or an
// unintended attribute.
func resolveSGRUnit(u []string, pal [16]color.Color) []string {
	n, ok := csiFieldInt(u[0])
	if !ok {
		return u
	}
	if len(u) == 1 {
		// 30-37 / 90-97 foreground, 40-47 / 100-107 background.
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
	// Only a colour introducer reaches here as more than one field, and sgrUnits
	// guarantees u[1] and every following field are integers when it grouped them.
	if (n == 38 || n == 48) && u[1] == "5" {
		idx := mustIntOrZero(u[2])
		switch {
		case idx < 0 || idx > 255:
			// Out of the defined range: opaque either way, left as written.
			return u
		case idx < 16:
			return trueColourUnit(n, pal[idx])
		default:
			r, g, b := xterm256ToRGB(idx)
			return rgbUnit(n, r, g, b)
		}
	}
	// 38;2;r;g;b / 48;2;r;g;b and anything else: already resolved or opaque.
	return u
}

// mustIntOrZero converts a field sgrUnits already vetted as an integer. It
// exists so the happy path needs no second error branch.
func mustIntOrZero(s string) int {
	n, _ := csiFieldInt(s)
	return n
}

// rgbUnit builds a 38;2;r;g;b or 48;2;r;g;b unit from raw channels.
func rgbUnit(prefix int, r, g, b uint8) []string {
	return []string{
		strconv.Itoa(prefix), "2",
		strconv.Itoa(int(r)), strconv.Itoa(int(g)), strconv.Itoa(int(b)),
	}
}

// trueColourUnit builds a 38;2;r;g;b or 48;2;r;g;b unit from a palette colour.
func trueColourUnit(prefix int, c color.Color) []string {
	r, g, b := rgbOf(c)
	return rgbUnit(prefix, r, g, b)
}

// resolveSGRParams transforms an SGR parameter list, resolving index colours
// against the palette. Attributes (bold, underline, ...) and true-colour
// parameters pass through untouched, as does every field that is not a plain
// integer.
func resolveSGRParams(params []string, pal [16]color.Color) []string {
	units := sgrUnits(params)
	out := make([]string, 0, len(params)+4)
	for _, u := range units {
		out = append(out, resolveSGRUnit(u, pal)...)
	}
	return out
}

// parseCSIParams splits a CSI parameter string ("1;31") into its fields, kept
// as their original text rather than converted. Most fields are integers, but
// the two shapes that are not matter just as much: the empty field, which SGR
// reads as the default (usually 0, so "\x1b[m" and "\x1b[;m" both mean reset),
// and colon-coded sub-parameters such as "4:3", which the resolver must hand
// back exactly as they arrived. Converting to numbers here once flattened
// "4:3" into 0, and a stray SGR 0 kills every attribute set before it.
func parseCSIParams(s string) []string {
	return strings.Split(s, ";")
}

// joinCSIParams renders a parameter list back into its "1;31" form. Fields
// rejoin exactly as they were split, empties and colons included, so a
// sequence the resolver did not rewrite comes back byte-identical.
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
		// Scan to the final byte of the CSI sequence.
		j := i + 2
		for j < len(content) && !(content[j] >= 0x40 && content[j] <= 0x7e) {
			j++
		}
		if j >= len(content) {
			// Unterminated sequence: hand the rest through untouched.
			b.WriteString(content[i:])
			break
		}
		if content[j] == 'm' {
			params := parseCSIParams(content[i+2 : j])
			resolved := resolveSGRParams(params, palette)
			if slices.Equal(resolved, params) {
				// Nothing to resolve: keep the sequence exactly as the
				// emulator emitted it, so a bare reset stays "\x1b[m" and
				// attribute-only sequences are not rewritten.
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
