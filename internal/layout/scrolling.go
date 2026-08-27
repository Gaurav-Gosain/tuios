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
	// Active is the index in WindowIDs of the window this column is focused on,
	// which is the window focus lands on when the column is focused.
	//
	// A column remembers it. Without it, focusing a column always focused its
	// top window: stack three, work in the bottom one, step right and step back,
	// and you were in the top one - which is niri's behaviour inverted, since
	// there a column's focus is exactly what stepping back returns you to.
	Active int
}

// activeID is the window a column is focused on, or -1 for an empty column.
func (c *ScrollColumn) activeID() int {
	if len(c.WindowIDs) == 0 {
		return -1
	}
	return c.WindowIDs[c.clampActive()]
}

// clampActive keeps Active inside WindowIDs after a window has left the column,
// and returns it.
func (c *ScrollColumn) clampActive() int {
	if c.Active < 0 {
		c.Active = 0
	}
	if c.Active >= len(c.WindowIDs) {
		c.Active = max(len(c.WindowIDs)-1, 0)
	}
	return c.Active
}

// ScrollingLayout manages the scrollable tiling strip.
type ScrollingLayout struct {
	Columns      []ScrollColumn
	FocusedCol   int       // Index of the focused column
	ViewportX    int       // Scroll offset in cells
	DefaultWidth float64   // Default column width proportion (e.g., 0.5)
	PresetWidths []float64 // Preset width proportions to cycle through
	// Gap is the cells of empty ground kept between neighbouring columns, and
	// between windows stacked inside one column. It is appearance.gap, set by
	// the caller on every access (see OS.GetOrCreateScrollingLayout) because it
	// is session state that a peer client can move mid-session.
	//
	// The strip's panes always draw their own borders, so this is ground and
	// never a divider: there is no separator overlay in scrolling mode for a
	// wider gap to thicken.
	Gap int
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
				// The window that left was above the active one, so the active
				// one has moved up a place. Removing the active window itself
				// leaves the index where it is, which is now the window below -
				// the same rule the column strip follows for a closed column.
				if j < s.Columns[i].Active {
					s.Columns[i].Active--
				}
				s.Columns[i].clampActive()
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
	// A prior keyboard resize pins FixedWidth, which resolveWidth prefers over
	// Proportion; clear it so the cycled preset proportion takes effect.
	col.FixedWidth = 0

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
	windowID := next.activeID()
	idx := next.clampActive()
	next.WindowIDs = append(next.WindowIDs[:idx], next.WindowIDs[idx+1:]...)
	next.clampActive()

	col := &s.Columns[s.FocusedCol]
	col.WindowIDs = append(col.WindowIDs, windowID)
	// Focus follows the window that moved, which is what makes consume and
	// expel undo each other.
	col.Active = len(col.WindowIDs) - 1

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
	// The window that leaves is the one the column is focused on, not whichever
	// happens to be at the bottom. Expelling the bottom window meant the action
	// moved a pane the user was not looking at, and it could not undo a consume.
	at := col.clampActive()
	windowID := col.WindowIDs[at]
	col.WindowIDs = append(col.WindowIDs[:at], col.WindowIDs[at+1:]...)
	col.clampActive()

	newCol := ScrollColumn{WindowIDs: []int{windowID}}
	idx := s.FocusedCol + 1
	s.Columns = append(s.Columns, ScrollColumn{})
	copy(s.Columns[idx+1:], s.Columns[idx:])
	s.Columns[idx] = newCol
	// Focus follows the window out, so the pane the user was in is still the
	// pane they are in.
	s.FocusedCol = idx
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

// scrollPeek is the cells of the neighbouring column kept on screen beside the
// focused one, so the strip says there is more of it in the direction you are
// travelling. Small on purpose: it is a hint that the strip continues, not a
// second column.
const scrollPeek = 4

// reveal scrolls the viewport by the least that brings the focused column into
// view with margin cells to spare on each side, and not at all when it is
// already there.
//
// Least-scroll rather than centring. Centring moves the whole strip on every
// step even when the column the user asked for was already half on screen, and
// a strip that jumps under a plain left/right press is the thing that makes a
// scrolling layout hard to keep your place in. It is also what niri does by
// default; centring every focus is its center-focused-column "always", which is
// opt-in there.
func (s *ScrollingLayout) reveal(screenWidth, margin int) {
	colX := s.columnX(s.FocusedCol, screenWidth)
	colW := s.resolveWidth(s.Columns[s.FocusedCol], screenWidth)

	// A column too wide to show with margin on both sides gets what is left,
	// shared; without this the two clauses below fight and the viewport lands
	// wherever the second one puts it.
	if colW+2*margin > screenWidth {
		margin = max((screenWidth-colW)/2, 0)
	}
	if colX-margin < s.ViewportX {
		s.ViewportX = colX - margin
	}
	if colX+colW+margin > s.ViewportX+screenWidth {
		s.ViewportX = colX + colW + margin - screenWidth
	}
	s.ClampViewport(screenWidth)
}

// EnsureFocusedVisible brings the focused column on screen if none of it is,
// and otherwise leaves the viewport where it is.
//
// The threshold is deliberately "nothing of it is visible" rather than "not all
// of it is": this runs on every retile and on every click, and a column the user
// can already see and click does not need the strip to move under them. Explicit
// keyboard navigation asks for more; that is ScrollToFocusedColumn.
func (s *ScrollingLayout) EnsureFocusedVisible(screenWidth int) {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return
	}
	colX := s.columnX(s.FocusedCol, screenWidth)
	colW := s.resolveWidth(s.Columns[s.FocusedCol], screenWidth)
	if colX < s.ViewportX+screenWidth && colX+colW > s.ViewportX {
		return // some of it is on screen
	}
	s.reveal(screenWidth, 0)
}

// ScrollToFocusedColumn shows the whole focused column, with a peek of its
// neighbour beside it. Used by explicit keyboard navigation (FocusLeft/Right,
// MoveColumn, CycleWidth, ResizeColumn), where the user asked to be taken to
// this column and wants to see all of it.
func (s *ScrollingLayout) ScrollToFocusedColumn(screenWidth int) {
	if s.FocusedCol < 0 || s.FocusedCol >= len(s.Columns) {
		return
	}
	s.reveal(screenWidth, scrollPeek)
}

// FocusColumnContaining sets focus to the column containing the given window ID.
// Returns true if the window was found. If not found, FocusedCol is unchanged
// and the caller should avoid scrolling the viewport.
func (s *ScrollingLayout) FocusColumnContaining(windowID int) bool {
	for ci := range s.Columns {
		if at := slices.Index(s.Columns[ci].WindowIDs, windowID); at >= 0 {
			s.FocusedCol = ci
			// Focusing a window in a stacked column is what tells the column
			// which of its windows it is on, so stepping away and back returns
			// to this one rather than to the top of the stack.
			s.Columns[ci].Active = at
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
		// Windows stacked in one column divide its height on the same terms
		// neighbouring columns divide the strip: the gap comes out first and the
		// remainder is spread rather than dropped on the last one.
		rows := spans(topMargin, usableHeight, windowCount, s.Gap)
		for j, winID := range col.WindowIDs {
			result[winID] = Rect{
				X: screenX,
				Y: rows[j].Pos,
				W: colWidth,
				H: rows[j].Size,
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
	return s.Columns[s.FocusedCol].activeID()
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
