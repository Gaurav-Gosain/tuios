// Package layout provides window tiling and layout management for the terminal.
// This file implements niri-style scrolling tiling where windows are arranged
// as columns on an infinite horizontal strip with a viewport.
package layout

import "slices"

// ScrollColumn represents a column in the scrolling layout.
// Each column contains one or more windows stacked vertically.
type ScrollColumn struct {
	WindowIDs  []int   // Windows stacked in this column
	Proportion float64 // Width as proportion of screen (0.0-1.0), 0 = default
	FixedWidth int     // Fixed width in cells (0 = use proportion)
}

// ScrollingLayout manages the scrollable tiling strip.
type ScrollingLayout struct {
	Columns        []ScrollColumn
	FocusedCol     int       // Index of the focused column
	ViewportX      int       // Scroll offset in cells
	DefaultWidth   float64   // Default column width proportion (e.g., 0.5)
	PresetWidths   []float64 // Preset width proportions to cycle through
	CenterMode     string    // "never", "always", "on-overflow"
	Gap            int       // Gap between columns in cells
}

// NewScrollingLayout creates a new scrolling layout with sensible defaults.
func NewScrollingLayout() *ScrollingLayout {
	return &ScrollingLayout{
		DefaultWidth: 0.5,
		PresetWidths: []float64{0.333, 0.5, 0.667, 1.0},
		CenterMode:   "on-overflow",
	}
}

// AddColumn inserts a new column after the focused column.
func (s *ScrollingLayout) AddColumn(windowID int) {
	col := ScrollColumn{
		WindowIDs: []int{windowID},
	}

	if s.FocusedCol >= len(s.Columns)-1 {
		s.Columns = append(s.Columns, col)
	} else {
		idx := s.FocusedCol + 1
		s.Columns = append(s.Columns, ScrollColumn{})
		copy(s.Columns[idx+1:], s.Columns[idx:])
		s.Columns[idx] = col
	}

	// Focus the new column
	if len(s.Columns) > 1 {
		s.FocusedCol++
	}
}

// RemoveWindow removes a window from the layout.
// If the column becomes empty, it's removed entirely.
func (s *ScrollingLayout) RemoveWindow(windowID int) {
	for i := range s.Columns {
		for j, id := range s.Columns[i].WindowIDs {
			if id == windowID {
				s.Columns[i].WindowIDs = append(
					s.Columns[i].WindowIDs[:j],
					s.Columns[i].WindowIDs[j+1:]...,
				)
				// Remove empty column
				if len(s.Columns[i].WindowIDs) == 0 {
					s.Columns = append(s.Columns[:i], s.Columns[i+1:]...)
					if s.FocusedCol >= len(s.Columns) && s.FocusedCol > 0 {
						s.FocusedCol--
					}
				}
				return
			}
		}
	}
}

// FocusLeft moves focus to the column to the left.
func (s *ScrollingLayout) FocusLeft() {
	if s.FocusedCol > 0 {
		s.FocusedCol--
	}
}

// FocusRight moves focus to the column to the right.
func (s *ScrollingLayout) FocusRight() {
	if s.FocusedCol < len(s.Columns)-1 {
		s.FocusedCol++
	}
}

// MoveColumnLeft swaps the focused column with the one to its left.
func (s *ScrollingLayout) MoveColumnLeft() {
	if s.FocusedCol > 0 {
		s.Columns[s.FocusedCol], s.Columns[s.FocusedCol-1] =
			s.Columns[s.FocusedCol-1], s.Columns[s.FocusedCol]
		s.FocusedCol--
	}
}

// MoveColumnRight swaps the focused column with the one to its right.
func (s *ScrollingLayout) MoveColumnRight() {
	if s.FocusedCol < len(s.Columns)-1 {
		s.Columns[s.FocusedCol], s.Columns[s.FocusedCol+1] =
			s.Columns[s.FocusedCol+1], s.Columns[s.FocusedCol]
		s.FocusedCol++
	}
}

// CycleWidth cycles the focused column through preset widths.
func (s *ScrollingLayout) CycleWidth() {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) || len(s.PresetWidths) == 0 {
		return
	}
	col := &s.Columns[s.FocusedCol]
	current := col.Proportion
	if current == 0 {
		current = s.DefaultWidth
	}

	// Find next preset
	for i, w := range s.PresetWidths {
		if w > current+0.01 { // Small epsilon for float comparison
			col.Proportion = s.PresetWidths[i]
			return
		}
	}
	// Wrap around to first preset
	col.Proportion = s.PresetWidths[0]
}

// ConsumeWindow moves the window from the next column into the focused column
// (stacks them vertically).
func (s *ScrollingLayout) ConsumeWindow() {
	if s.FocusedCol >= len(s.Columns)-1 {
		return
	}
	next := &s.Columns[s.FocusedCol+1]
	if len(next.WindowIDs) == 0 {
		return
	}
	// Move the first window from the next column into the focused column
	windowID := next.WindowIDs[0]
	next.WindowIDs = next.WindowIDs[1:]
	s.Columns[s.FocusedCol].WindowIDs = append(s.Columns[s.FocusedCol].WindowIDs, windowID)

	// Remove next column if empty
	if len(next.WindowIDs) == 0 {
		s.Columns = append(s.Columns[:s.FocusedCol+1], s.Columns[s.FocusedCol+2:]...)
	}
}

// ExpelWindow moves the last window from the focused column into a new column to its right.
func (s *ScrollingLayout) ExpelWindow() {
	col := &s.Columns[s.FocusedCol]
	if len(col.WindowIDs) < 2 {
		return
	}
	// Remove last window
	windowID := col.WindowIDs[len(col.WindowIDs)-1]
	col.WindowIDs = col.WindowIDs[:len(col.WindowIDs)-1]

	// Insert as new column to the right
	newCol := ScrollColumn{WindowIDs: []int{windowID}}
	idx := s.FocusedCol + 1
	s.Columns = append(s.Columns, ScrollColumn{})
	copy(s.Columns[idx+1:], s.Columns[idx:])
	s.Columns[idx] = newCol
}

// resolveWidth returns the width in cells for a column.
func (s *ScrollingLayout) resolveWidth(col ScrollColumn, screenWidth int) int {
	if col.FixedWidth > 0 {
		return col.FixedWidth
	}
	proportion := col.Proportion
	if proportion <= 0 {
		proportion = s.DefaultWidth
	}
	return max(int(float64(screenWidth)*proportion), 10)
}

// columnX returns the X position of a column on the virtual canvas.
func (s *ScrollingLayout) columnX(index, screenWidth int) int {
	x := 0
	for i := 0; i < index && i < len(s.Columns); i++ {
		x += s.resolveWidth(s.Columns[i], screenWidth) + s.Gap
	}
	return x
}

// EnsureFocusedVisible adjusts the viewport to keep the focused column visible.
func (s *ScrollingLayout) EnsureFocusedVisible(screenWidth int) {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return
	}

	colX := s.columnX(s.FocusedCol, screenWidth)
	colWidth := s.resolveWidth(s.Columns[s.FocusedCol], screenWidth)

	switch s.CenterMode {
	case "always":
		s.ViewportX = colX - (screenWidth-colWidth)/2
	case "on-overflow":
		if colX < s.ViewportX || colX+colWidth > s.ViewportX+screenWidth {
			s.ViewportX = colX - (screenWidth-colWidth)/2
		}
	default: // "never"
		if colX < s.ViewportX {
			s.ViewportX = colX
		} else if colX+colWidth > s.ViewportX+screenWidth {
			s.ViewportX = colX + colWidth - screenWidth
		}
	}

	if s.ViewportX < 0 {
		s.ViewportX = 0
	}
}

// ApplyLayout computes positions for all visible columns.
// Returns a map of windowID -> Rect (only for visible windows).
func (s *ScrollingLayout) ApplyLayout(screenWidth, usableHeight, topMargin int) map[int]Rect {
	result := make(map[int]Rect)
	if len(s.Columns) == 0 {
		return result
	}

	s.EnsureFocusedVisible(screenWidth)

	x := 0
	for _, col := range s.Columns {
		colWidth := s.resolveWidth(col, screenWidth)
		screenX := x - s.ViewportX

		// Skip if entirely off-screen
		if screenX+colWidth <= 0 || screenX >= screenWidth {
			x += colWidth + s.Gap
			continue
		}

		// Only render fully visible columns (simpler, avoids content clipping)
		if screenX < 0 || screenX+colWidth > screenWidth {
			x += colWidth + s.Gap
			continue
		}

		// Stack windows vertically within column
		windowCount := len(col.WindowIDs)
		if windowCount == 0 {
			x += colWidth + s.Gap
			continue
		}
		cellHeight := usableHeight / windowCount
		for j, winID := range col.WindowIDs {
			h := cellHeight
			if j == windowCount-1 {
				h = usableHeight - j*cellHeight
			}
			result[winID] = Rect{
				X: screenX,
				Y: topMargin + j*cellHeight,
				W: colWidth,
				H: h,
			}
		}
		x += colWidth + s.Gap
	}

	return result
}

// WindowCount returns the total number of windows across all columns.
func (s *ScrollingLayout) WindowCount() int {
	count := 0
	for _, col := range s.Columns {
		count += len(col.WindowIDs)
	}
	return count
}

// GetFocusedWindowID returns the first window ID in the focused column.
func (s *ScrollingLayout) GetFocusedWindowID() int {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return -1
	}
	col := s.Columns[s.FocusedCol]
	if len(col.WindowIDs) == 0 {
		return -1
	}
	return col.WindowIDs[0]
}

// HasWindow checks if a window is in the layout.
func (s *ScrollingLayout) HasWindow(windowID int) bool {
	for _, col := range s.Columns {
		if slices.Contains(col.WindowIDs, windowID) {
			return true
		}
	}
	return false
}
