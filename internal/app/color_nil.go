package app

import (
	"image/color"
	"reflect"
)

// isNilColor reports whether c carries no drawable color: either an untyped-nil
// color.Color interface, or a color.Color that wraps a nil pointer such as
// (*color.RGBA)(nil).
//
// color.Color's RGBA method has a value receiver, so invoking it through an
// interface that holds a nil pointer does not return zeros — it panics with
// "value method image/color.RGBA.RGBA called using nil *RGBA pointer". Terminal
// cells occasionally arrive with such wrapped-nil style colors, so every RGBA()
// call on a cell style color must screen for this first. The common colors
// (lipgloss.Color, lipgloss.ANSIColor, color.RGBA, …) are non-pointer kinds and
// return false after a single cheap Kind() check.
func isNilColor(c color.Color) bool {
	if c == nil {
		return true
	}
	rv := reflect.ValueOf(c)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// safeColorEquals reports whether two cell style colors are visually equal
// without panicking on wrapped-nil colors (see isNilColor). Adjacent cells
// almost always share the same color interface value, so identity is compared
// before falling back to the four RGBA computations.
func safeColorEquals(a, b color.Color) bool {
	if a == b {
		return true
	}
	if isNilColor(a) || isNilColor(b) {
		return false
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
