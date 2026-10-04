package vt

import (
	"strconv"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// mouseEncoding is the wire form of a mouse report, chosen by DEC private
// modes 1005, 1006, 1015 and 1016. Without any of them a report is the X10
// form: three bytes after CSI M.
type mouseEncoding int

const (
	mouseEncX10      mouseEncoding = iota // CSI M Cb Cx Cy, one byte each
	mouseEncUTF8                          // 1005: CSI M Cb, then Cx and Cy as UTF-8
	mouseEncURXVT                         // 1015: CSI Cb ; Cx ; Cy M, in decimal
	mouseEncSGR                           // 1006: CSI < Cb ; Cx ; Cy M or m
	mouseEncSGRPixel                      // 1016: the SGR form, in pixels
)

// pickMouseEncoding chooses among the encodings a guest has turned on. xterm
// uses the last one set. The mode map does not record order, so the richer
// encoding wins, which is the one a guest that set two of them reads best:
// every program that turns on 1006 parses it, whatever else it also set.
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

// The largest coordinate each byte-oriented encoding can carry, zero-based.
// A coordinate travels as 32 + 1 + value: one byte in the X10 form, so at most
// 255, and one UTF-8 character of at most two bytes in the 1005 form, so at
// most 2047. xterm has the same limits.
const (
	mouseX10Limit  = 255 - 32 - 1
	mouseUTF8Limit = 2047 - 32 - 1
)

// mouseReport is one mouse event, ready to encode.
type mouseReport struct {
	button           MouseButton
	shift, alt, ctrl bool
	motion, release  bool
	x, y             int // zero-based cell, or pixel for SGR-pixel
	x10Only          bool
	encoding         mouseEncoding
}

// encode returns the bytes a guest is sent for the event, or "" when the
// event cannot be reported in the guest's encoding.
//
// The byte-oriented encodings cannot carry every event:
//
//   - A coordinate past the encoding's limit is not reported. Writing it
//     anyway wrapped the byte, so a click in column 250 reached the guest as
//     an ESC and started a new escape sequence in its input.
//   - The X10 form writes each coordinate as one raw byte. A value of 128 or
//     more went out as a two-byte UTF-8 character, which is the 1005 encoding
//     and not the one the guest asked for.
//   - A release has no button in any form but SGR: it is reported as button
//     3, with the modifiers, as xterm does.
//
// Mode 9, X10 compatibility, reports presses only, and without modifiers.
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
		// Wheel notches have no release to report.
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
		out := []byte{0x1b, '[', 'M', 32 + b}
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
