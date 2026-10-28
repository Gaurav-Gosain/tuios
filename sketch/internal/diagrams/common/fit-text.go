// Ported from pele src/diagrams/common/fit-text.ts at 8aff183be57021b533f319a29341a529be934b08.

package common

import (
	"math"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/text"
)

// Fitted is one line of text fitted into a width.
type Fitted struct {
	Text  string
	Size  float64
	Width float64
}

// FitText fits one line of text into a width: first by shrinking the font down to `min`, then by cutting
// the text and adding an ellipsis. It returns false when not even one character fits.
func FitText(t string, max, size, min float64, measurer text.TextMeasurer, style int) (Fitted, bool) {
	if t == "" || max <= 0 {
		return Fitted{}, false
	}
	width := measurer.Width(t, size, style)
	if width <= max {
		return Fitted{t, size, width}, true
	}

	// Width grows with font size, so one division lands on the right size or just above it.
	fitted := math.Max(min, math.Floor((size*max)/width))
	for fitted >= min {
		width = measurer.Width(t, fitted, style)
		if width <= max {
			return Fitted{t, fitted, width}, true
		}
		fitted--
	}

	// Array.from(text): code points, which a rune of Go stands for.
	chars := []rune(t)
	low := 0
	high := len(chars) - 1
	for low < high {
		mid := (low + high + 1) >> 1
		if measurer.Width(js.TrimEnd(string(chars[:mid]))+"…", min, style) <= max {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low == 0 {
		return Fitted{}, false
	}
	cut := js.TrimEnd(string(chars[:low])) + "…"
	width = measurer.Width(cut, min, style)
	if width <= max {
		return Fitted{cut, min, width}, true
	}
	return Fitted{}, false
}
