package vt

import "sync"

// SixelState manages sixel image placements for a terminal screen.
// Unlike Kitty graphics, sixel images don't have IDs - they're placed
// inline at the cursor position and scroll with text.
type SixelState struct {
	mu         sync.Mutex
	placements []*SixelPlacement

	// Callback for notifying when placements change
	changeCallback func()
}

// SixelPlacement represents a sixel image placed in the terminal.
type SixelPlacement struct {
	// AbsoluteLine is the line in scrollback where the image starts
	// (0 = first line ever written to terminal)
	AbsoluteLine int

	// ScreenX is the column position where the image was placed
	ScreenX int

	// Width is the image width in pixels
	Width int

	// Height is the image height in pixels
	Height int

	// Rows is the number of terminal rows the image occupies
	Rows int

	// Cols is the number of terminal columns the image occupies
	Cols int

	// Data contains the raw sixel data for passthrough/re-rendering
	Data []byte

	// RawSequence contains the complete DCS sequence
	RawSequence []byte

	// AspectRatio from the original command
	AspectRatio int

	// BackgroundMode from the original command
	BackgroundMode int
}

// NewSixelState creates a new SixelState.
func NewSixelState() *SixelState {
	return &SixelState{
		placements: make([]*SixelPlacement, 0),
	}
}

// SetChangeCallback sets a callback to be called when placements change.
func (s *SixelState) SetChangeCallback(cb func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changeCallback = cb
}

// AddPlacement adds a new sixel placement.
func (s *SixelState) AddPlacement(p *SixelPlacement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placements = append(s.placements, p)
	if s.changeCallback != nil {
		s.changeCallback()
	}
}

// GetPlacements returns a copy of all placements.
func (s *SixelState) GetPlacements() []*SixelPlacement {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*SixelPlacement, len(s.placements))
	copy(result, s.placements)
	return result
}

// GetVisiblePlacements returns placements that are visible in the current viewport.
// scrollbackLen is the total number of lines in scrollback.
// scrollOffset is how many lines the user has scrolled back (0 = at bottom).
// viewportHeight is the number of visible rows.
func (s *SixelState) GetVisiblePlacements(scrollbackLen, scrollOffset, viewportHeight int) []*SixelPlacement {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Calculate the range of absolute lines that are visible
	viewportTop := scrollbackLen - scrollOffset - viewportHeight
	viewportBottom := scrollbackLen - scrollOffset

	var visible []*SixelPlacement
	for _, p := range s.placements {
		placementBottom := p.AbsoluteLine + p.Rows

		// Check if placement overlaps with viewport
		if placementBottom > viewportTop && p.AbsoluteLine < viewportBottom {
			visible = append(visible, p)
		}
	}

	return visible
}

// Clear removes all placements.
func (s *SixelState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placements = make([]*SixelPlacement, 0)
	if s.changeCallback != nil {
		s.changeCallback()
	}
}

// ClearScrolledOut removes placements that have scrolled past a certain line.
// This helps prevent memory buildup for long-running sessions.
func (s *SixelState) ClearScrolledOut(minLine int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var remaining []*SixelPlacement
	for _, p := range s.placements {
		// Keep placements that end after minLine
		if p.AbsoluteLine+p.Rows > minLine {
			remaining = append(remaining, p)
		}
	}

	if len(remaining) != len(s.placements) {
		s.placements = remaining
		if s.changeCallback != nil {
			s.changeCallback()
		}
	}
}

// RemovePlacementsInRange removes placements that start within the given line range.
// Used when text is erased or overwritten.
func (s *SixelState) RemovePlacementsInRange(startLine, endLine int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var remaining []*SixelPlacement
	for _, p := range s.placements {
		// Keep placements that don't start in the range
		if p.AbsoluteLine < startLine || p.AbsoluteLine >= endLine {
			remaining = append(remaining, p)
		}
	}

	if len(remaining) != len(s.placements) {
		s.placements = remaining
		if s.changeCallback != nil {
			s.changeCallback()
		}
	}
}

// Count returns the number of placements.
func (s *SixelState) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.placements)
}
