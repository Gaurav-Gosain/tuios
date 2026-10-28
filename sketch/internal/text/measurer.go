// Ported from pele src/text/measurer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The canvas measurer is browser only and is not ported. defaultMeasurer always
// gives the metrics measurer, which is what pele uses outside a browser.

// Package text measures and lays out label text.
package text

import (
	"strconv"
	"sync"
)

// Style bits of a span of text.
const (
	Bold   = 1
	Italic = 2
	Mono   = 4
	Sub    = 8
	Sup    = 16
	// Underline is the style of <u> and <ins>.
	Underline = 32
)

// smaller is the approximate divisor for CSS font-size="smaller".
const smaller = 1.2

// TextMeasurer gives the width of text at a font size in a style.
type TextMeasurer interface {
	Width(text string, size float64, style int) float64
}

func unpack(packed string) *[95]uint16 {
	var out [95]uint16
	for i := 0; i < 95; i++ {
		v, _ := strconv.ParseUint(packed[i*2:i*2+2], 36, 16)
		out[i] = uint16(v)
	}
	return &out
}

var (
	tablesOnce sync.Once
	regular    *[95]uint16
	bold       *[95]uint16
)

func isWide(c rune) bool {
	return (c >= 0x1100 && c <= 0x115f) ||
		(c >= 0x2e80 && c <= 0xa4cf) ||
		(c >= 0xac00 && c <= 0xd7a3) ||
		(c >= 0xf900 && c <= 0xfaff) ||
		(c >= 0xfe30 && c <= 0xfe4f) ||
		(c >= 0xff00 && c <= 0xff60) ||
		(c >= 0xffe0 && c <= 0xffe6) ||
		(c >= 0xd800 && c <= 0xdbff)
}

func isZeroWidth(c rune) bool {
	return c == 0xad ||
		(c >= 0x300 && c <= 0x36f) ||
		(c >= 0x200b && c <= 0x200f) ||
		(c >= 0xdc00 && c <= 0xdfff) ||
		(c >= 0xfe00 && c <= 0xfe0f) ||
		c == 0xfeff
}

type metrics struct{}

// MetricsMeasurer estimates widths from a table of Arial advances. Used where no canvas is available.
var MetricsMeasurer TextMeasurer = metrics{}

func (metrics) Width(text string, size float64, style int) float64 {
	tablesOnce.Do(func() {
		regular = unpack(REGULAR)
		bold = unpack(BOLD)
	})
	mono := style&Mono != 0
	eff := size
	if style&(Sub|Sup) != 0 {
		eff = size / smaller
	}
	table := regular
	if style&Bold != 0 {
		table = bold
	}
	units := 0
	for _, c := range text {
		// A character past the BMP is two UTF-16 units in pele: a high surrogate,
		// which counts as wide, and a low one, which counts as nothing.
		if c > 0xffff {
			c = 0xd800
		}
		if c < 32 {
			continue
		}
		if c < 127 {
			if mono {
				units += 600
			} else {
				units += int(table[c-32])
			}
		} else if isZeroWidth(c) {
			continue
		} else if isWide(c) {
			if mono {
				units += 1200
			} else {
				units += 1000
			}
		} else if mono {
			units += 600
		} else {
			units += 580
		}
	}
	return (float64(units) * eff) / 1000
}

// DefaultMeasurer is the measurer used when the host gives none.
func DefaultMeasurer(fontFamily, monoFamily string) TextMeasurer {
	return MetricsMeasurer
}
