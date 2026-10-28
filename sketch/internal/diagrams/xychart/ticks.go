// Ported from pele src/diagrams/xychart/ticks.ts at 8aff183be57021b533f319a29341a529be934b08.

package xychart

import (
	"math"

	"tuios.dev/sketch/internal/js"
)

// pow10 is 10 ** y for a y that Math.floor gave: whole, or infinite, or NaN.
func pow10(y float64) float64 {
	switch {
	case math.IsNaN(y):
		return math.NaN()
	case y > 400:
		return math.Inf(1)
	case y < -400:
		return 0
	}
	return js.Pow10(int(y))
}

// tickStep is a step of 1, 2 or 5 times a power of ten that divides `span` into about `count` parts.
// A step below one comes back as the negative of its inverse, which is a whole number, so that
// tick values are found by division and print without rounding noise (0.3, not 0.30000000000000004).
func tickStep(span, count float64) float64 {
	raw := span / math.Max(count, 1)
	power := math.Floor(js.Log10(raw))
	unit := raw / pow10(power)
	factor := 1.0
	if unit >= 7.07 {
		factor = 10
	} else if unit >= 3.16 {
		factor = 5
	} else if unit >= 1.41 {
		factor = 2
	}
	if power < 0 {
		return -pow10(-power) / factor
	}
	return factor * pow10(power)
}

func tickIndex(step, value float64) float64 {
	if step > 0 {
		return value / step
	}
	return value * -step
}

func tickValue(step, index float64) float64 {
	if step > 0 {
		return index * step
	}
	return index / -step
}

// ticksBetween gives the multiples of a step from `lo` to `hi`, both included.
func ticksBetween(lo, hi, step float64) []float64 {
	first := js.Round(tickIndex(step, lo))
	if tickValue(step, first) < lo {
		first++
	}
	last := js.Round(tickIndex(step, hi))
	if tickValue(step, last) > hi {
		last--
	}
	var out []float64
	for i := first; i <= last && len(out) < 100; i++ {
		out = append(out, tickValue(step, i))
	}
	return out
}
