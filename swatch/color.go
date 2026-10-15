package swatch

import (
	"image/color"
	"strings"
)

// ParseHex reads a colour written #rrggbb or #rgb, in either case. Anything
// else, including an alpha channel, is not a colour here.
func ParseHex(s string) (color.RGBA, bool) {
	if !strings.HasPrefix(s, "#") {
		return color.RGBA{}, false
	}
	s = s[1:]
	var v [6]byte
	switch len(s) {
	case 6:
		for i := range 6 {
			d, ok := hexDigit(s[i])
			if !ok {
				return color.RGBA{}, false
			}
			v[i] = d
		}
	case 3:
		for i := range 3 {
			d, ok := hexDigit(s[i])
			if !ok {
				return color.RGBA{}, false
			}
			v[2*i], v[2*i+1] = d, d
		}
	default:
		return color.RGBA{}, false
	}
	return color.RGBA{R: v[0]<<4 | v[1], G: v[2]<<4 | v[3], B: v[4]<<4 | v[5], A: 0xff}, true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Hex spells a colour as #rrggbb in lower case, and nil as the empty string.
// Alpha is dropped.
func Hex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	const digits = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out[1+2*i] = digits[v>>4]
		out[2+2*i] = digits[v&0xf]
	}
	return string(out)
}
