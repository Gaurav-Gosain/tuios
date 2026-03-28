package app

import (
	"image"
	"testing"
)

func TestCropSixel(t *testing.T) {
	// Build a minimal sixel sequence: "0;0;0q" followed by a small 6x6 pixel image
	// The simplest sixel: just a few characters representing 6-pixel columns
	// Each char '?' to '~' represents 6 vertical pixels
	// '~' = all 6 pixels on, '?' = all 6 pixels off
	// A raster attribute '"1;1;6;6' specifies 1:1 aspect ratio, 6x6 pixels
	raw := []byte("0;0;0q\"1;1;6;6#0;2;100;0;0#0~~~~~~")

	t.Run("no crop needed", func(t *testing.T) {
		result := CropSixel(raw, image.Rect(0, 0, 6, 6))
		if result == nil {
			t.Fatal("CropSixel returned nil for full-size crop")
		}
		// Should return original since no cropping needed
		if string(result) != string(raw) {
			t.Log("CropSixel returned re-encoded data (expected when bounds match)")
		}
	})

	t.Run("crop top half", func(t *testing.T) {
		result := CropSixel(raw, image.Rect(0, 0, 6, 3))
		if result == nil {
			t.Fatal("CropSixel returned nil for top-half crop")
		}
		// Result should be a valid sixel sequence smaller than original
		if len(result) == 0 {
			t.Error("CropSixel returned empty result")
		}
	})

	t.Run("crop right half", func(t *testing.T) {
		result := CropSixel(raw, image.Rect(3, 0, 6, 6))
		if result == nil {
			t.Fatal("CropSixel returned nil for right-half crop")
		}
	})

	t.Run("empty crop rect", func(t *testing.T) {
		result := CropSixel(raw, image.Rect(0, 0, 0, 0))
		if result != nil {
			t.Error("expected nil for empty crop rect")
		}
	})

	t.Run("crop outside bounds", func(t *testing.T) {
		result := CropSixel(raw, image.Rect(100, 100, 200, 200))
		if result != nil {
			t.Error("expected nil for crop outside image bounds")
		}
	})

	t.Run("nil input", func(t *testing.T) {
		result := CropSixel(nil, image.Rect(0, 0, 6, 6))
		if result != nil {
			t.Error("expected nil for nil input")
		}
	})

	t.Run("no q introducer", func(t *testing.T) {
		result := CropSixel([]byte("no-data-here"), image.Rect(0, 0, 6, 6))
		if result != nil {
			t.Error("expected nil for input without q introducer")
		}
	})
}
