package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// Scrollback represents a scrollback buffer that stores lines that have
// scrolled off the top of the visible screen.
type Scrollback struct {
	// lines stores the scrollback lines, with the oldest at index 0
	lines [][]uv.Cell
	// maxLines is the maximum number of lines to keep in scrollback
	maxLines int
}

// NewScrollback creates a new scrollback buffer with the specified maximum
// number of lines. If maxLines is 0, a default of 10000 lines is used.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = 10000 // Default scrollback size
	}
	return &Scrollback{
		lines:    make([][]uv.Cell, 0, min(maxLines, 1000)), // Pre-allocate reasonable amount
		maxLines: maxLines,
	}
}

// PushLine adds a line to the scrollback buffer. If the buffer is full,
// the oldest line is removed.
func (sb *Scrollback) PushLine(line []uv.Cell) {
	if len(line) == 0 {
		return
	}

	// Make a copy of the line to avoid aliasing issues
	lineCopy := make([]uv.Cell, len(line))
	copy(lineCopy, line)

	// If we're at capacity, remove the oldest line
	if len(sb.lines) >= sb.maxLines {
		sb.lines = sb.lines[1:]
	}

	sb.lines = append(sb.lines, lineCopy)
}

// Len returns the number of lines currently in the scrollback buffer.
func (sb *Scrollback) Len() int {
	return len(sb.lines)
}

// Line returns the line at the specified index in the scrollback buffer.
// Index 0 is the oldest line, and Len()-1 is the newest (most recently scrolled).
// Returns nil if the index is out of bounds.
func (sb *Scrollback) Line(index int) []uv.Cell {
	if index < 0 || index >= len(sb.lines) {
		return nil
	}
	return sb.lines[index]
}

// Lines returns a slice of all lines in the scrollback buffer, from oldest
// to newest. The returned slice should not be modified.
func (sb *Scrollback) Lines() [][]uv.Cell {
	return sb.lines
}

// Clear removes all lines from the scrollback buffer.
func (sb *Scrollback) Clear() {
	sb.lines = sb.lines[:0] // Keep capacity, just reset length
}

// MaxLines returns the maximum number of lines this scrollback can hold.
func (sb *Scrollback) MaxLines() int {
	return sb.maxLines
}

// SetMaxLines sets the maximum number of lines for the scrollback buffer.
// If the new limit is smaller than the current number of lines, older lines
// are discarded to fit the new limit.
func (sb *Scrollback) SetMaxLines(maxLines int) {
	if maxLines <= 0 {
		maxLines = 10000 // Default scrollback size
	}
	sb.maxLines = maxLines

	// If we have too many lines, trim from the front (oldest)
	if len(sb.lines) > maxLines {
		sb.lines = sb.lines[len(sb.lines)-maxLines:]
	}
}

// extractLine extracts a complete line from the buffer at the given Y coordinate.
// This is a helper function to copy cells from a buffer line.
func extractLine(buf *uv.Buffer, y, width int) []uv.Cell {
	line := make([]uv.Cell, width)
	for x := 0; x < width; x++ {
		if cell := buf.CellAt(x, y); cell != nil {
			line[x] = *cell
		} else {
			line[x] = uv.EmptyCell
		}
	}
	return line
}
