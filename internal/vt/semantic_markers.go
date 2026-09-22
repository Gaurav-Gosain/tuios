package vt

import "sync"

// SemanticMarkerType represents an OSC 133 semantic zone marker type.
type SemanticMarkerType byte

const (
	// MarkerPromptStart is 'A': prompt start
	MarkerPromptStart SemanticMarkerType = 'A'
	// MarkerCommandStart is 'B': command input start (after prompt)
	MarkerCommandStart SemanticMarkerType = 'B'
	// MarkerCommandExecuted is 'C': command execution start (output begins)
	MarkerCommandExecuted SemanticMarkerType = 'C'
	// MarkerCommandFinished is 'D': command finished (exit code available)
	MarkerCommandFinished SemanticMarkerType = 'D'
)

// SemanticMarker represents a single OSC 133 marker captured from the terminal.
type SemanticMarker struct {
	Type         SemanticMarkerType
	AbsLine      int    // scrollbackLen + cursorY at time of emission
	Col          int    // cursor X (column) at time of emission
	ExitCode     int    // only meaningful for 'D', -1 = unknown
	CapturedText string // command text captured at C-marker time (before output)
}

// SemanticMarkerList is a thread-safe, bounded list of semantic markers.
type SemanticMarkerList struct {
	mu       sync.Mutex
	markers  []SemanticMarker
	maxItems int
}

// NewSemanticMarkerList creates a new marker list with the given capacity.
func NewSemanticMarkerList(maxItems int) *SemanticMarkerList {
	if maxItems <= 0 {
		maxItems = 10000
	}
	return &SemanticMarkerList{
		markers:  make([]SemanticMarker, 0, 256),
		maxItems: maxItems,
	}
}

// Add appends a marker to the list, discarding the oldest if at capacity.
func (l *SemanticMarkerList) Add(m SemanticMarker) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.markers) >= l.maxItems {
		// Discard oldest 10% to avoid frequent shifts
		trim := max(l.maxItems/10, 1)
		l.markers = l.markers[trim:]
	}
	l.markers = append(l.markers, m)
}

// Markers returns a copy of all markers.
func (l *SemanticMarkerList) Markers() []SemanticMarker {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SemanticMarker, len(l.markers))
	copy(out, l.markers)
	return out
}

// Len returns the number of markers.
func (l *SemanticMarkerList) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.markers)
}

// Clear removes all markers.
func (l *SemanticMarkerList) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.markers = l.markers[:0]
}

// Last returns the most recent marker of the given type, or nil if none.
func (l *SemanticMarkerList) Last(t SemanticMarkerType) *SemanticMarker {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.markers) - 1; i >= 0; i-- {
		if l.markers[i].Type == t {
			return &l.markers[i]
		}
	}
	return nil
}

// RemoveOnScreen removes markers whose AbsLine >= scrollbackLen, i.e. markers
// that reference visible screen content. Used when the screen is cleared (CSI 2J)
// so that stale on-screen markers don't cause output extraction to read
// overwritten content after new commands run.
func (l *SemanticMarkerList) RemoveOnScreen(scrollbackLen int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for i := range l.markers {
		if l.markers[i].AbsLine < scrollbackLen {
			l.markers[n] = l.markers[i]
			n++
		}
	}
	l.markers = l.markers[:n]
}

// AdjustForScrollbackTrim adjusts all marker AbsLine values when scrollback
// lines are trimmed from the ring buffer. Markers that fall before the new
// origin are removed.
func (l *SemanticMarkerList) AdjustForScrollbackTrim(linesRemoved int) {
	if linesRemoved <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for i := range l.markers {
		l.markers[i].AbsLine -= linesRemoved
		if l.markers[i].AbsLine >= 0 {
			l.markers[n] = l.markers[i]
			n++
		}
	}
	l.markers = l.markers[:n]
}
