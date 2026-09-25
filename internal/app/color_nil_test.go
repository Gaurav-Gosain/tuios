package app

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestWrappedNilColourDoesNotPanic is the regression guard for issue #124. A
// cell whose style colour is a color.Color holding (*color.RGBA)(nil) crashed
// the whole app with "value method image/color.RGBA.RGBA called using nil *RGBA
// pointer": RGBA has a value receiver, so calling it through such an interface
// panics rather than returning zeros. Cells arrive that way, so every site
// that reads a cell colour screens with isNilColor.
func TestWrappedNilColourDoesNotPanic(t *testing.T) {
	var wrappedNil color.Color = (*color.RGBA)(nil)
	realCol := color.RGBA{R: 10, G: 20, B: 30, A: 255}

	t.Run("isNilColor", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			c    color.Color
			want bool
		}{
			{"untyped nil interface", nil, true},
			{"wrapped nil *color.RGBA", wrappedNil, true},
			{"real color.RGBA value", realCol, false},
			{"lipgloss.Color", lipgloss.Color("#ff8700"), false},
		} {
			if got := isNilColor(tc.c); got != tc.want {
				t.Errorf("%s: isNilColor = %v, want %v", tc.name, got, tc.want)
			}
		}
	})

	t.Run("safeColorEquals", func(t *testing.T) {
		if safeColorEquals(wrappedNil, realCol) || safeColorEquals(realCol, wrappedNil) {
			t.Error("wrapped-nil and a real colour compared equal")
		}
		if !safeColorEquals(realCol, color.RGBA{R: 10, G: 20, B: 30, A: 255}) {
			t.Error("identical colours should be equal")
		}
		if safeColorEquals(realCol, color.RGBA{R: 99, G: 20, B: 30, A: 255}) {
			t.Error("differing colours should not be equal")
		}
		// Two wrapped-nil colours of the same dynamic type are equal by
		// identity and must not reach the panicking RGBA() call.
		if !safeColorEquals(wrappedNil, (*color.RGBA)(nil)) {
			t.Error("two wrapped-nil colours of the same type should be equal")
		}
	})

	t.Run("hashCellAttrs", func(t *testing.T) {
		sc := NewStyleCache(16)
		cell := &uv.Cell{
			Content: "x",
			Width:   1,
			Style:   uv.Style{Fg: wrappedNil, Bg: wrappedNil, UnderlineColor: wrappedNil},
		}
		_ = sc.hashCellAttrs(cell, false)
	})

	t.Run("dimCell", func(t *testing.T) {
		var dst, src uv.Cell
		src.Content = "x"
		src.Style.Fg, src.Style.Bg = wrappedNil, wrappedNil
		fg, bg := color.RGBA{R: 200, G: 200, B: 200, A: 255}, color.RGBA{R: 20, G: 20, B: 30, A: 255}
		got := dimCell(&dst, &src, fg, bg, 0.5)
		if got == nil {
			t.Fatal("dimCell returned nil")
		}
		if isNilColor(got.Style.Fg) {
			t.Error("a wrapped-nil fg was not replaced by the ground's own ink")
		}
	})
}
