package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestScrollbackRingBuffer(t *testing.T) {
	sb := NewScrollback(5)

	// Test initial state
	if sb.Len() != 0 {
		t.Errorf("expected empty scrollback, got %d lines", sb.Len())
	}
	if sb.MaxLines() != 5 {
		t.Errorf("expected maxLines=5, got %d", sb.MaxLines())
	}

	// Push lines until full
	for i := range 5 {
		line := uv.Line{{Content: string(rune('A' + i)), Width: 1}}
		sb.PushLine(line)
	}

	if sb.Len() != 5 {
		t.Errorf("expected 5 lines, got %d", sb.Len())
	}

	// Verify ring buffer overwrites oldest
	line6 := uv.Line{{Content: "F", Width: 1}}
	sb.PushLine(line6)

	if sb.Len() != 5 {
		t.Errorf("expected 5 lines after overflow, got %d", sb.Len())
	}

	// First line should now be 'B' (oldest 'A' was dropped)
	first := sb.Line(0)
	if first == nil || first[0].Content != "B" {
		t.Errorf("expected first line to be 'B', got %v", first)
	}

	// Last line should be 'F'
	last := sb.Line(4)
	if last == nil || last[0].Content != "F" {
		t.Errorf("expected last line to be 'F', got %v", last)
	}
}

func TestScrollbackSoftWrapping(t *testing.T) {
	sb := NewScrollback(10)

	// Push soft-wrapped line
	line1 := uv.Line{{Content: "A", Width: 1}}
	sb.PushLineWithWrap(line1, true)

	// Push hard break line
	line2 := uv.Line{{Content: "B", Width: 1}}
	sb.PushLineWithWrap(line2, false)

	if sb.Len() != 2 {
		t.Errorf("expected 2 lines, got %d", sb.Len())
	}

	// Verify lines were stored
	if l := sb.Line(0); l == nil || l[0].Content != "A" {
		t.Errorf("line 0 incorrect")
	}
	if l := sb.Line(1); l == nil || l[0].Content != "B" {
		t.Errorf("line 1 incorrect")
	}
}

func TestScrollbackClear(t *testing.T) {
	sb := NewScrollback(10)

	for i := range 5 {
		line := uv.Line{{Content: string(rune('A' + i)), Width: 1}}
		sb.PushLine(line)
	}

	sb.Clear()

	if sb.Len() != 0 {
		t.Errorf("expected empty after clear, got %d lines", sb.Len())
	}

	// Should be able to push after clear
	line := uv.Line{{Content: "X", Width: 1}}
	sb.PushLine(line)

	if sb.Len() != 1 {
		t.Errorf("expected 1 line after push, got %d", sb.Len())
	}
}

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

func TestScrollbackBoundsChecking(t *testing.T) {
	sb := NewScrollback(5)

	line := uv.Line{{Content: "A", Width: 1}}
	sb.PushLine(line)

	// Out of bounds access should return nil
	if sb.Line(-1) != nil {
		t.Error("expected nil for negative index")
	}
	if sb.Line(100) != nil {
		t.Error("expected nil for out of bounds index")
	}

	// Valid access should work
	if sb.Line(0) == nil {
		t.Error("expected valid line at index 0")
	}
}

func TestScrollbackWidthTracking(t *testing.T) {
	sb := NewScrollback(10)

	if sb.CaptureWidth() != 0 {
		t.Errorf("expected initial width 0, got %d", sb.CaptureWidth())
	}

	sb.SetCaptureWidth(80)
	if sb.CaptureWidth() != 80 {
		t.Errorf("expected width 80, got %d", sb.CaptureWidth())
	}

	// Reflow should update width
	sb.Reflow(100)
	if sb.CaptureWidth() != 100 {
		t.Errorf("expected width 100 after reflow, got %d", sb.CaptureWidth())
	}
}

func TestScrollbackEmptyPushIgnored(t *testing.T) {
	sb := NewScrollback(5)

	// Empty line should be ignored
	sb.PushLine(uv.Line{})

	if sb.Len() != 0 {
		t.Errorf("expected empty scrollback after pushing empty line, got %d", sb.Len())
	}
}
