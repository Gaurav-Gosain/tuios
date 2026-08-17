package app

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

func TestIsNilColor(t *testing.T) {
	var wrappedNil color.Color = (*color.RGBA)(nil)
	cases := []struct {
		name string
		c    color.Color
		want bool
	}{
		{"untyped nil interface", nil, true},
		{"wrapped nil *color.RGBA", wrappedNil, true},
		{"real color.RGBA value", color.RGBA{R: 1, G: 2, B: 3, A: 4}, false},
		{"lipgloss.Color", lipgloss.Color("#ff8700"), false},
	}
	for _, tc := range cases {
		if got := isNilColor(tc.c); got != tc.want {
			t.Errorf("%s: isNilColor = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSafeColorEqualsWrappedNilDoesNotPanic is the regression guard for
// issue #124: a terminal cell whose style color is a wrapped-nil pointer
// (a color.Color interface holding (*color.RGBA)(nil)) previously crashed the
// whole app with "value method image/color.RGBA.RGBA called using nil *RGBA
// pointer" the moment its color was compared during rendering.
func TestSafeColorEqualsWrappedNilDoesNotPanic(t *testing.T) {
	var wrappedNil color.Color = (*color.RGBA)(nil)
	realCol := color.RGBA{R: 10, G: 20, B: 30, A: 255}

	if safeColorEquals(wrappedNil, realCol) {
		t.Error("wrapped-nil vs real color should not be equal")
	}
	if safeColorEquals(realCol, wrappedNil) {
		t.Error("real vs wrapped-nil color should not be equal")
	}
	if !safeColorEquals(realCol, color.RGBA{R: 10, G: 20, B: 30, A: 255}) {
		t.Error("identical colors should be equal")
	}
	if safeColorEquals(realCol, color.RGBA{R: 99, G: 20, B: 30, A: 255}) {
		t.Error("differing colors should not be equal")
	}
	// Two wrapped-nil colors of the same dynamic type are equal by identity and
	// must not reach the panicking RGBA() call.
	if !safeColorEquals(wrappedNil, (*color.RGBA)(nil)) {
		t.Error("two wrapped-nil colors of the same type should be equal")
	}
}

// TestHashCellAttrsWrappedNilDoesNotPanic covers the second site that reads a
// cell style color's RGBA() after only an interface-nil check. A wrapped-nil
// color must hash like an absent color instead of panicking.
func TestHashCellAttrsWrappedNilDoesNotPanic(t *testing.T) {
	sc := NewStyleCache(16)
	cell := &uv.Cell{
		Content: "x",
		Width:   1,
		Style:   uv.Style{Fg: (*color.RGBA)(nil), Bg: (*color.RGBA)(nil)},
	}
	_ = sc.hashCellAttrs(cell, false, false)
}
