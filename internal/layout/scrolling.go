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
	Columns      []ScrollColumn
	FocusedCol   int       // Index of the focused column
	ViewportX    int       // Scroll offset in cells
	DefaultWidth float64   // Default column width proportion (e.g., 0.5)
	PresetWidths []float64 // Preset width proportions to cycle through
	Gap          int       // Gap between columns in cells
}

// NewScrollingLayout creates a new scrolling layout with sensible defaults.
func NewScrollingLayout() *ScrollingLayout {
	return &ScrollingLayout{
		DefaultWidth: 0.55,
		PresetWidths: []float64{0.333, 0.5, 0.55, 0.667, 0.9},
	}
}

// AddColumn inserts a new column after the focused column and focuses it.
func (s *ScrollingLayout) AddColumn(windowID int) {
	col := ScrollColumn{
		WindowIDs: []int{windowID},
	}

	insertIdx := len(s.Columns) // default: append at end
	if len(s.Columns) > 0 && s.FocusedCol < len(s.Columns)-1 {
		insertIdx = s.FocusedCol + 1
		s.Columns = append(s.Columns, ScrollColumn{})
		copy(s.Columns[insertIdx+1:], s.Columns[insertIdx:])
		s.Columns[insertIdx] = col
	} else {
		s.Columns = append(s.Columns, col)
	}

	s.FocusedCol = insertIdx
}

// RemoveWindow removes a window from the layout.
// If the column becomes empty, it's removed and focus shifts LEFT.
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
					removedIdx := i
					s.Columns = append(s.Columns[:i], s.Columns[i+1:]...)
					// Focus the column to the LEFT of the removed one
					if s.FocusedCol >= removedIdx && s.FocusedCol > 0 {
						s.FocusedCol--
					}
					if s.FocusedCol >= len(s.Columns) && len(s.Columns) > 0 {
						s.FocusedCol = len(s.Columns) - 1
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
		if w > current+0.01 {
			col.Proportion = s.PresetWidths[i]
			return
		}
	}
	col.Proportion = s.PresetWidths[0]
}

// ConsumeWindow moves the window from the next column into the focused column.
func (s *ScrollingLayout) ConsumeWindow() {
	if s.FocusedCol >= len(s.Columns)-1 {
		return
	}
	next := &s.Columns[s.FocusedCol+1]
	if len(next.WindowIDs) == 0 {
		return
	}
	windowID := next.WindowIDs[0]
	next.WindowIDs = next.WindowIDs[1:]
	s.Columns[s.FocusedCol].WindowIDs = append(s.Columns[s.FocusedCol].WindowIDs, windowID)

	if len(next.WindowIDs) == 0 {
		s.Columns = append(s.Columns[:s.FocusedCol+1], s.Columns[s.FocusedCol+2:]...)
	}
}

// ExpelWindow moves the last window from the focused column into a new column.
func (s *ScrollingLayout) ExpelWindow() {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return
	}
	col := &s.Columns[s.FocusedCol]
	if len(col.WindowIDs) < 2 {
		return
	}
	windowID := col.WindowIDs[len(col.WindowIDs)-1]
	col.WindowIDs = col.WindowIDs[:len(col.WindowIDs)-1]

	newCol := ScrollColumn{WindowIDs: []int{windowID}}
	idx := s.FocusedCol + 1
	s.Columns = append(s.Columns, ScrollColumn{})
	copy(s.Columns[idx+1:], s.Columns[idx:])
	s.Columns[idx] = newCol
}

// ResolveColumnWidth returns the width in cells for a column by index.
func (s *ScrollingLayout) ResolveColumnWidth(colIndex, screenWidth int) int {
	if colIndex < 0 || colIndex >= len(s.Columns) {
		return 0
	}
	return s.resolveWidth(s.Columns[colIndex], screenWidth)
}

// resolveWidth returns the width in cells for a column, capped at 90% of screen.
func (s *ScrollingLayout) resolveWidth(col ScrollColumn, screenWidth int) int {
	maxWidth := screenWidth * 9 / 10
	if col.FixedWidth > 0 {
		return min(col.FixedWidth, maxWidth)
	}
	proportion := col.Proportion
	if proportion <= 0 {
		proportion = s.DefaultWidth
	}
	return min(max(int(float64(screenWidth)*proportion), 10), maxWidth)
}

// TotalStripWidth returns the total width of all columns in cells.
func (s *ScrollingLayout) TotalStripWidth(screenWidth int) int {
	total := 0
	for i, col := range s.Columns {
		total += s.resolveWidth(col, screenWidth)
		if i < len(s.Columns)-1 {
			total += s.Gap
		}
	}
	return total
}

// columnX returns the X position of a column on the virtual strip.
func (s *ScrollingLayout) columnX(index, screenWidth int) int {
	x := 0
	for i := 0; i < index && i < len(s.Columns); i++ {
		x += s.resolveWidth(s.Columns[i], screenWidth) + s.Gap
	}
	return x
}

// ClampViewport ensures the viewport doesn't scroll past the content.
func (s *ScrollingLayout) ClampViewport(screenWidth int) {
	maxScroll := max(s.TotalStripWidth(screenWidth)-screenWidth, 0)
	if s.ViewportX < 0 {
		s.ViewportX = 0
	}
	if s.ViewportX > maxScroll {
		s.ViewportX = maxScroll
	}
}

// EnsureFocusedVisible only scrolls the viewport when the focused column is
// COMPLETELY off-screen. If any part of the column is already visible (the
// user can see and interact with it), the viewport stays put. This prevents
// the jarring large-jump behavior when clicking a partially-visible edge
// column or when TileAllWindows runs during resize/retile.
//
// For explicit keyboard navigation where the user WANTS to see the full
// column, use ScrollToFocusedColumn instead.
func (s *ScrollingLayout) EnsureFocusedVisible(screenWidth int) {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return
	}
	colX := s.columnX(s.FocusedCol, screenWidth)
	colW := s.resolveWidth(s.Columns[s.FocusedCol], screenWidth)

	fullyVisible := colX >= s.ViewportX && colX+colW <= s.ViewportX+screenWidth
	if fullyVisible {
		return
	}

	// Center the focused column so both neighbors peek in
	s.ViewportX = colX - (screenWidth-colW)/2
	s.ClampViewport(screenWidth)
}

// ScrollToFocusedColumn scrolls the viewport to fully show the focused column
// with a small margin to peek at neighbors. Used by explicit keyboard
// navigation (FocusLeft/Right, MoveColumn, CycleWidth, ResizeColumn).
func (s *ScrollingLayout) ScrollToFocusedColumn(screenWidth int) {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return
	}
	colX := s.columnX(s.FocusedCol, screenWidth)
	colW := s.resolveWidth(s.Columns[s.FocusedCol], screenWidth)

	s.ViewportX = colX - (screenWidth-colW)/2
	s.ClampViewport(screenWidth)
}

// FocusColumnContaining sets focus to the column containing the given window ID.
// Returns true if the window was found. If not found, FocusedCol is unchanged
// and the caller should avoid scrolling the viewport.
func (s *ScrollingLayout) FocusColumnContaining(windowID int) bool {
	for ci, col := range s.Columns {
		if slices.Contains(col.WindowIDs, windowID) {
			s.FocusedCol = ci
			return true
		}
	}
	return false
}

// ComputePositions computes positions for ALL columns using current ViewportX.
// Pure function  - does NOT modify ViewportX. Caller must call EnsureFocusedVisible
// and ClampViewport beforehand if needed.
func (s *ScrollingLayout) ComputePositions(screenWidth, usableHeight, topMargin int) map[int]Rect {
	result := make(map[int]Rect)
	if len(s.Columns) == 0 {
		return result
	}

	x := 0
	for _, col := range s.Columns {
		colWidth := s.resolveWidth(col, screenWidth)
		screenX := x - s.ViewportX

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
