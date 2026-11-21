package vt

import (
	"strconv"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

type mouseEncoding int

const (
	mouseEncX10 mouseEncoding = iota
	mouseEncUTF8
	mouseEncURXVT
	mouseEncSGR
	mouseEncSGRPixel
)

func pickMouseEncoding(utf8Mode, urxvt, sgr, sgrPixel bool) mouseEncoding {
	switch {
	case sgrPixel:
		return mouseEncSGRPixel
	case sgr:
		return mouseEncSGR
	case urxvt:
		return mouseEncURXVT
	case utf8Mode:
		return mouseEncUTF8
	default:
		return mouseEncX10
	}
}

const (
	mouseX10Limit  = 255 - 32 - 1
	mouseUTF8Limit = 2047 - 32 - 1
)

type mouseReport struct {
	button           MouseButton
	shift, alt, ctrl bool
	motion, release  bool
	x, y             int
	x10Only          bool
	encoding         mouseEncoding
}

func (r mouseReport) encode() string {
	shift, alt, ctrl := r.shift, r.alt, r.ctrl
	if r.x10Only {
		if r.release || r.motion {
			return ""
		}
		shift, alt, ctrl = false, false, false
	}
	b := ansi.EncodeMouseButton(r.button, r.motion, shift, alt, ctrl)
	if b == 0xff {
		return ""
	}
	if r.release && r.encoding != mouseEncSGR && r.encoding != mouseEncSGRPixel {
		if r.button >= MouseWheelUp && r.button <= MouseWheelRight {
			return ""
		}
		b = b&^0b1100_0011 | 0b11
	}
	x, y := max(r.x, 0), max(r.y, 0)

	switch r.encoding {
	case mouseEncSGR, mouseEncSGRPixel:
		return ansi.MouseSgr(b, x, y, r.release)
	case mouseEncURXVT:
		return "\x1b[" + strconv.Itoa(32+int(b)) + ";" + strconv.Itoa(x+1) + ";" + strconv.Itoa(y+1) + "M"
	case mouseEncUTF8:
		if x > mouseUTF8Limit || y > mouseUTF8Limit {
			return ""
		}
		out := []byte{0x1b, '[', 'M'}
		out = utf8.AppendRune(out, rune(32+int(b)))
		out = utf8.AppendRune(out, rune(32+1+x))
		out = utf8.AppendRune(out, rune(32+1+y))
		return string(out)
	default:
		if x > mouseX10Limit || y > mouseX10Limit {
			return ""
		}
		return string([]byte{0x1b, '[', 'M', 32 + b, byte(32 + 1 + x), byte(32 + 1 + y)})
	}
}
