package session

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// This file implements resolved colour capture for capture-pane.
//
// The daemon-side emulator stores colours exactly as the guest sent them: a
// program that emits SGR 31 leaves an ansi.BasicColor(1) in the cell, and
// Render() re-emits 31. That is the intended contract — appearance is
// client-owned, and a consumer of capture-pane resolves indices against its
// own palette. But a client that wants to pipe a capture into a tool which
// renders verbatim (no palette of its own) has no way to get the colours it
// actually paints. ResolveSGR is that way: it rewrites SGR index colours to
// 24-bit RGB using the palette the client supplies, which is the client's
// theme palette. The daemon still knows nothing about themes; the palette is
// an explicit parameter of the request.

// xtermPalette returns the standard xterm 16-colour palette as RGB, used when
// a resolved capture arrives without an explicit palette. The daemon has no
// theme, so this is the honest default: better than emitting indices for a
// consumer that cannot resolve them, and identical for every caller.
func xtermPalette() [16]color.Color {
	var pal [16]color.Color
	for i := 0; i < 16; i++ {
		r, g, b, _ := ansi.BasicColor(i).RGBA()
		pal[i] = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}
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

// sgrUnits splits an SGR parameter list into units, where a unit is one
// parameter together with any it extends: 38;5;n and 38;2;r;g;b each form a
// single unit, as do 48;5;n and 48;2;r;g;b. Everything else is a unit of one.
func sgrUnits(params []int) [][]int {
	var units [][]int
	for i := 0; i < len(params); i++ {
		switch {
		case params[i] == 38 || params[i] == 48:
			// Colour: either 38;5;n (indexed) or 38;2;r;g;b (true colour).
			if i+2 < len(params) && params[i+1] == 5 {
				units = append(units, []int{params[i], 5, params[i+2]})
				i += 2
			} else if i+4 < len(params) && params[i+1] == 2 {
				units = append(units, []int{params[i], 2, params[i+2], params[i+3], params[i+4]})
				i += 4
			} else {
				units = append(units, []int{params[i]})
			}
		default:
			units = append(units, []int{params[i]})
		}
	}
	return units
}

// resolveSGRUnit rewrites one SGR unit to 24-bit RGB when it is an index
// colour the palette can resolve, and returns it unchanged otherwise.
func resolveSGRUnit(u []int, pal [16]color.Color) []int {
	if len(u) == 1 {
		// 30-37 / 90-97 foreground, 40-47 / 100-107 background.
		switch {
		case u[0] >= 30 && u[0] <= 37:
			return trueColourUnit(38, pal[u[0]-30])
		case u[0] >= 40 && u[0] <= 47:
			return trueColourUnit(48, pal[u[0]-40])
		case u[0] >= 90 && u[0] <= 97:
			return trueColourUnit(38, pal[u[0]-90+8])
		case u[0] >= 100 && u[0] <= 107:
			return trueColourUnit(48, pal[u[0]-100+8])
		}
		return u
	}
	if len(u) == 3 && u[1] == 5 {
		// 38;5;n / 48;5;n indexed: resolve only the first sixteen, which are
		// the palette the theme controls. Higher indices are the standard
		// 256-colour cube/ramp every consumer resolves identically, so they
		// are left as indices.
		if u[2] < 16 {
			return trueColourUnit(u[0], pal[u[2]])
		}
		return u
	}
	// 38;2;r;g;b / 48;2;r;g;b and anything else: already resolved or opaque.
	return u
}

// trueColourUnit builds a 38;2;r;g;b or 48;2;r;g;b unit from a palette colour.
func trueColourUnit(prefix int, c color.Color) []int {
	r, g, b := rgbOf(c)
	return []int{prefix, 2, int(r), int(g), int(b)}
}

// resolveSGRParams transforms an SGR parameter list, resolving index colours
// against the palette. Attributes (bold, underline, ...) and true-colour
// parameters pass through untouched.
func resolveSGRParams(params []int, pal [16]color.Color) []int {
	units := sgrUnits(params)
	out := make([]int, 0, len(params))
	for _, u := range units {
		out = append(out, resolveSGRUnit(u, pal)...)
	}
	return out
}

// parseCSIParams splits a CSI parameter string ("1;31") into ints. Empty
// fields — which SGR treats as the default, usually 0 — become 0, so
// "\x1b[m" and "\x1b[;m" both come out as a single reset.
func parseCSIParams(s string) []int {
	if s == "" {
		return []int{0}
	}
	fields := strings.Split(s, ";")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			out = append(out, 0)
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			// A non-numeric parameter is not valid SGR; keep it as 0 so the
			// sequence still parses as a reset rather than corrupting output.
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}

// joinCSIParams renders a parameter list back into its "1;31" form. An empty
// list is the bare reset, exactly as SGR reads it.
func joinCSIParams(params []int) string {
	if len(params) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.Itoa(p))
	}
	return b.String()
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
