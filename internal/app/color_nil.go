package app

import (
	"image/color"
	"reflect"
)

func isNilColor(c color.Color) bool {
	if c == nil {
		return true
	}
	rv := reflect.ValueOf(c)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

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
