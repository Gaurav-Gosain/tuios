package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultScrollbackSize is the default number of lines to keep in the
// scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback represents a scrollback buffer that stores lines that have
// scrolled off the top of the visible screen.
// Uses a ring buffer for O(1) insertions instead of O(n) slice reallocations.
// Supports soft-wrapping to handle terminal resizes gracefully.
type Scrollback struct {
	// lines stores the scrollback lines in a ring buffer
	lines []uv.Line
	// maxLines is the maximum number of lines to keep in scrollback
	maxLines int
	// head is the index of the oldest line in the ring buffer
	head int
	// tail is the index where the next line will be inserted
	tail int
	// full indicates whether the ring buffer is at capacity
	full bool
	// lastWidthCaptured tracks the terminal width when lines were last added
	// Used for detecting when reflow is needed on resize
	lastWidthCaptured int
	// softWrapped indicates which lines are soft-wrapped (not hard breaks)
	// A soft-wrapped line can be reflowed to a different width
	softWrapped []bool
}

// NewScrollback creates a new scrollback buffer with the specified maximum
// number of lines. If maxLines is 0, DefaultScrollbackSize is used.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{
		lines:             make([]uv.Line, maxLines), // Pre-allocate full ring buffer
		maxLines:          maxLines,
		head:              0,
		tail:              0,
		full:              false,
		lastWidthCaptured: 0,
		softWrapped:       make([]bool, maxLines), // Track which lines are soft-wrapped
	}
}

// PushLine adds a line to the scrollback buffer. If the buffer is full,
// the oldest line is removed (by overwriting it in the ring buffer).
// This is now an O(1) operation instead of O(n).
// The isSoftWrapped parameter indicates if this line is a soft-wrap (can be
// reflowed to a different width) or a hard break (actual newline from output).
func (sb *Scrollback) PushLine(line uv.Line) {
	sb.PushLineWithWrap(line, true) // Default to soft-wrapped for backwards compatibility
}

// PushLineWithWrap adds a line with wrap information for soft-wrap support.
func (sb *Scrollback) PushLineWithWrap(line uv.Line, isSoftWrapped bool) {
	if len(line) == 0 {
		return
	}

	// Make a copy of the line to avoid aliasing issues
	lineCopy := make(uv.Line, len(line))
	copy(lineCopy, line)

	// Insert at tail position
	sb.lines[sb.tail] = lineCopy
	sb.softWrapped[sb.tail] = isSoftWrapped

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
func (sb *Scrollback) Line(index int) uv.Line {
	length := sb.Len()
	if index < 0 || index >= length {
		return nil
	}
	if sb.maxLines <= 0 {
		return nil
	}
	// Map logical index to physical ring buffer index
	physicalIndex := (sb.head + index) % sb.maxLines
	if physicalIndex < 0 || physicalIndex >= len(sb.lines) {
		return nil
	}
	return sb.lines[physicalIndex]
}

// Lines returns a slice of all lines in the scrollback buffer, from oldest
// to newest. The returned slice should not be modified.
func (sb *Scrollback) Lines() []uv.Line {
	length := sb.Len()
	if length == 0 {
		return nil
	}

	// Build a slice in correct order from the ring buffer
	result := make([]uv.Line, length)
	for i := range length {
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
		sb.softWrapped[i] = false
	}
}

// Reflow reconstructs scrollback lines for a different terminal width.
// This handles the case where the terminal was resized and scrollback
// lines need to be re-wrapped to match the new width.
// This is a complex operation that should be called sparingly (only on resize).
func (sb *Scrollback) Reflow(newWidth int) {
	if newWidth <= 0 || sb.lastWidthCaptured == 0 || newWidth == sb.lastWidthCaptured {
		return // No reflow needed if width hasn't changed or is invalid
	}

	// For now, we mark that a width change happened but don't reflow lines
	// This is because reflowing lines while preserving ANSI styles is complex
	// and may not be worth the performance cost for every resize
	// Instead, applications should handle their own reflow via SIGWINCH
	//
	// TODO: Future optimization - implement intelligent reflow that:
	// 1. Groups soft-wrapped lines back together
	// 2. Re-wraps them at the new width
	// 3. Preserves ANSI color/style information through the rewrap
	// For now, just update the recorded width to prevent flickering
	sb.lastWidthCaptured = newWidth
}

// SetCaptureWidth sets the terminal width at which scrollback lines are being captured.
// Should be called from the emulator when processing output.
func (sb *Scrollback) SetCaptureWidth(width int) {
	if width > 0 && width != sb.lastWidthCaptured {
		// Width changed - could trigger reflow if implemented
		sb.lastWidthCaptured = width
	}
}

// CaptureWidth returns the terminal width at which scrollback was captured.
func (sb *Scrollback) CaptureWidth() int {
	return sb.lastWidthCaptured
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
		maxLines = DefaultScrollbackSize
	}

	if maxLines == sb.maxLines {
		return // No change needed
	}

	oldLen := sb.Len()
	if oldLen == 0 {
		// Empty buffer, just resize
		sb.lines = make([]uv.Line, maxLines)
		sb.softWrapped = make([]bool, maxLines)
		sb.maxLines = maxLines
		sb.head = 0
		sb.tail = 0
		sb.full = false
		return
	}

	// Create new ring buffer and copy existing lines
	newLines := make([]uv.Line, maxLines)
	newSoftWrapped := make([]bool, maxLines)
	newLen := min(oldLen, maxLines)

	// Copy the most recent newLen lines
	startIndex := oldLen - newLen // Skip oldest lines if downsizing
	for i := range newLen {
		physicalIndex := (sb.head + startIndex + i) % sb.maxLines
		newLines[i] = sb.lines[physicalIndex]
		newSoftWrapped[i] = sb.softWrapped[physicalIndex]
	}

	sb.lines = newLines
	sb.softWrapped = newSoftWrapped
	sb.maxLines = maxLines
	sb.head = 0
	sb.tail = newLen % maxLines
	sb.full = (newLen == maxLines)
}

// extractLine extracts a complete line from the buffer at the given Y coordinate.
// This is a helper function to copy cells from a buffer line.
func extractLine(buf *uv.Buffer, y, width int) uv.Line {
	line := make(uv.Line, width)
	for x := range width {
		if cell := buf.CellAt(x, y); cell != nil {
			line[x] = *cell
		} else {
			line[x] = uv.EmptyCell
		}
	}
	return line
}
