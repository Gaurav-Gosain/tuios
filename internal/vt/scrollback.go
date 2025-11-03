package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// Scrollback represents a scrollback buffer that stores lines that have
// scrolled off the top of the visible screen.
// Uses a ring buffer for O(1) insertions instead of O(n) slice reallocations.
type Scrollback struct {
	// lines stores the scrollback lines in a ring buffer
	lines [][]uv.Cell
	// maxLines is the maximum number of lines to keep in scrollback
	maxLines int
	// head is the index of the oldest line in the ring buffer
	head int
	// tail is the index where the next line will be inserted
	tail int
	// full indicates whether the ring buffer is at capacity
	full bool
}

// NewScrollback creates a new scrollback buffer with the specified maximum
// number of lines. If maxLines is 0, a default of 10000 lines is used.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = 10000 // Default scrollback size
	}
	return &Scrollback{
		lines:    make([][]uv.Cell, maxLines), // Pre-allocate full ring buffer
		maxLines: maxLines,
		head:     0,
		tail:     0,
		full:     false,
	}
}

// PushLine adds a line to the scrollback buffer. If the buffer is full,
// the oldest line is removed (by overwriting it in the ring buffer).
// This is now an O(1) operation instead of O(n).
func (sb *Scrollback) PushLine(line []uv.Cell) {
	if len(line) == 0 {
		return
	}

	// Make a copy of the line to avoid aliasing issues
	lineCopy := make([]uv.Cell, len(line))
	copy(lineCopy, line)

	// Insert at tail position
	sb.lines[sb.tail] = lineCopy

	// Advance tail (wraps around at maxLines)
	sb.tail = (sb.tail + 1) % sb.maxLines

	// If buffer is full, advance head (oldest line pointer) as well
	if sb.full {
		sb.head = (sb.head + 1) % sb.maxLines
	}

	// Mark as full when tail catches up to head
	if sb.tail == sb.head && len(lineCopy) > 0 {
		sb.full = true
	}
}

// Len returns the number of lines currently in the scrollback buffer.
func (sb *Scrollback) Len() int {
	if sb.full {
		return sb.maxLines
	}
	if sb.tail >= sb.head {
		return sb.tail - sb.head
	}
	return sb.maxLines - sb.head + sb.tail
}

// Line returns the line at the specified index in the scrollback buffer.
// Index 0 is the oldest line, and Len()-1 is the newest (most recently scrolled).
// Returns nil if the index is out of bounds.
func (sb *Scrollback) Line(index int) []uv.Cell {
	length := sb.Len()
	if index < 0 || index >= length {
		return nil
	}
	// Map logical index to physical ring buffer index
	physicalIndex := (sb.head + index) % sb.maxLines
	return sb.lines[physicalIndex]
}

// Lines returns a slice of all lines in the scrollback buffer, from oldest
// to newest. The returned slice should not be modified.
func (sb *Scrollback) Lines() [][]uv.Cell {
	length := sb.Len()
	if length == 0 {
		return nil
	}

	// Build a slice in correct order from the ring buffer
	result := make([][]uv.Cell, length)
	for i := 0; i < length; i++ {
		physicalIndex := (sb.head + i) % sb.maxLines
		result[i] = sb.lines[physicalIndex]
	}
	return result
}

// Clear removes all lines from the scrollback buffer.
func (sb *Scrollback) Clear() {
	sb.head = 0
	sb.tail = 0
	sb.full = false
	// Optionally nil out the lines to help GC, but keep the slice
	for i := range sb.lines {
		sb.lines[i] = nil
	}
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

	if maxLines == sb.maxLines {
		return // No change needed
	}

	oldLen := sb.Len()
	if oldLen == 0 {
		// Empty buffer, just resize
		sb.lines = make([][]uv.Cell, maxLines)
		sb.maxLines = maxLines
		sb.head = 0
		sb.tail = 0
		sb.full = false
		return
	}

	// Create new ring buffer and copy existing lines
	newLines := make([][]uv.Cell, maxLines)
	newLen := min(oldLen, maxLines)

	// Copy the most recent newLen lines
	startIndex := oldLen - newLen // Skip oldest lines if downsizing
	for i := 0; i < newLen; i++ {
		physicalIndex := (sb.head + startIndex + i) % sb.maxLines
		newLines[i] = sb.lines[physicalIndex]
	}

	sb.lines = newLines
	sb.maxLines = maxLines
	sb.head = 0
	sb.tail = newLen % maxLines
	sb.full = (newLen == maxLines)
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
