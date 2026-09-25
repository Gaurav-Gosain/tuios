package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestScrollbackSetMaxLines(t *testing.T) {
	sb := NewScrollback(10)

	// Fill with 8 lines
	for i := range 8 {
		line := uv.Line{{Content: string(rune('A' + i)), Width: 1}}
		sb.PushLine(line)
	}

	// Reduce max to 5 (should keep last 5: D,E,F,G,H)
	sb.SetMaxLines(5)

	if sb.Len() != 5 {
		t.Errorf("expected 5 lines after resize, got %d", sb.Len())
	}

	if sb.MaxLines() != 5 {
		t.Errorf("expected maxLines=5, got %d", sb.MaxLines())
	}

	// First line should be 'D' (oldest 3 dropped)
	first := sb.Line(0)
	if first == nil || first[0].Content != "D" {
		t.Errorf("expected first line to be 'D', got %v", first)
	}

	// Last line should be 'H'
	last := sb.Line(4)
	if last == nil || last[0].Content != "H" {
		t.Errorf("expected last line to be 'H', got %v", last)
	}
}

// TestSetMaxLinesDownsizeCallsOnTrim verifies that shrinking the scrollback
// fires onTrim with the number of dropped lines, so semantic markers stay
// re-based to the oldest remaining line.
func TestSetMaxLinesDownsizeCallsOnTrim(t *testing.T) {
	sb := NewScrollback(10)

	trimmed := 0
	sb.SetOnTrim(func(n int) { trimmed += n })

	// Fill 6 lines.
	for range 6 {
		sb.PushLine(uv.Line{uv.Cell{Content: "x", Width: 1}})
	}
	if sb.Len() != 6 {
		t.Fatalf("expected 6 lines, got %d", sb.Len())
	}

	// Shrink to hold only 4; the 2 oldest lines are dropped.
	sb.SetMaxLines(4)

	if sb.Len() != 4 {
		t.Fatalf("expected 4 lines after downsize, got %d", sb.Len())
	}
	if trimmed != 2 {
		t.Errorf("onTrim total = %d, want 2 (oldest lines dropped on downsize)", trimmed)
	}
}
