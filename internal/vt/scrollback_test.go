package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestScrollbackSetMaxLines checks that shrinking the ring keeps the newest
// lines and fires onTrim with the number dropped, so semantic markers stay
// re-based to the oldest remaining line.
func TestScrollbackSetMaxLines(t *testing.T) {
	sb := NewScrollback(10)
	trimmed := 0
	sb.SetOnTrim(func(n int) { trimmed += n })

	for i := range 8 {
		sb.PushLine(uv.Line{{Content: string(rune('A' + i)), Width: 1}})
	}

	// Keep the last 5 of A..H: D, E, F, G, H.
	sb.SetMaxLines(5)

	if sb.Len() != 5 || sb.MaxLines() != 5 {
		t.Errorf("after shrinking: %d lines, max %d, want 5 and 5", sb.Len(), sb.MaxLines())
	}
	if first := sb.Line(0); first == nil || first[0].Content != "D" {
		t.Errorf("first line is %v, want D", first)
	}
	if last := sb.Line(4); last == nil || last[0].Content != "H" {
		t.Errorf("last line is %v, want H", last)
	}
	if trimmed != 3 {
		t.Errorf("onTrim total = %d, want 3", trimmed)
	}
}
