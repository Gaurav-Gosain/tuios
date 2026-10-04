package vt

import (
	"sync"
	"sync/atomic"
)

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
//
// A marker's line is kept relative to an origin that only moves forward:
// the list stores AbsLine plus trimmed, the number of lines the scrollback
// had dropped when the marker was added, and every reader subtracts the
// current trimmed again. A scrollback trim then only moves the origin. It
// used to rewrite every marker's line under the lock, once per line a full
// ring dropped, which on a flooding pane was 8% of the daemon's time and grew
// with the number of markers.
//
// A marker whose line has fallen below the origin is gone. The trim drops
// those at the front, and readers skip any left behind later in the list (a
// marker can follow one on a lower line when the cursor moved up between
// them).
type SemanticMarkerList struct {
	mu       sync.Mutex
	markers  []SemanticMarker // AbsLine is stored plus trimmed
	trimmed  int
	maxItems int
	// nonEmpty mirrors len(markers) > 0, so a trim on a pane that has no
	// markers, which is most of them, takes no lock. Written under mu.
	nonEmpty atomic.Bool
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

// setMarkersLocked replaces the stored slice and keeps nonEmpty in step.
// Callers hold mu.
func (l *SemanticMarkerList) setMarkersLocked(markers []SemanticMarker) {
	l.markers = markers
	l.nonEmpty.Store(len(markers) > 0)
}

// Add appends a marker to the list, discarding the oldest if at capacity.
func (l *SemanticMarkerList) Add(m SemanticMarker) {
	l.mu.Lock()
	defer l.mu.Unlock()
	markers := l.markers
	if len(markers) >= l.maxItems {
		// Discard oldest 10% to avoid frequent shifts
		trim := max(l.maxItems/10, 1)
		markers = markers[trim:]
	}
	m.AbsLine += l.trimmed
	l.setMarkersLocked(append(markers, m))
}

// Markers returns a copy of all markers.
func (l *SemanticMarkerList) Markers() []SemanticMarker {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SemanticMarker, 0, len(l.markers))
	for _, m := range l.markers {
		if m.AbsLine -= l.trimmed; m.AbsLine >= 0 {
			out = append(out, m)
		}
	}
	return out
}

// replace swaps in a list the caller built from Markers, such as one a
// reflow moved.
func (l *SemanticMarkerList) replace(markers []SemanticMarker) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range markers {
		markers[i].AbsLine += l.trimmed
	}
	l.setMarkersLocked(markers)
}

// Len returns the number of markers.
func (l *SemanticMarkerList) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for i := range l.markers {
		if l.markers[i].AbsLine >= l.trimmed {
			n++
		}
	}
	return n
}

// Clear removes all markers.
func (l *SemanticMarkerList) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.setMarkersLocked(l.markers[:0])
}

// Last returns a copy of the most recent marker of the given type, or nil if
// none.
func (l *SemanticMarkerList) Last(t SemanticMarkerType) *SemanticMarker {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.markers) - 1; i >= 0; i-- {
		if m := l.markers[i]; m.Type == t && m.AbsLine >= l.trimmed {
			m.AbsLine -= l.trimmed
			return &m
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
		if line := l.markers[i].AbsLine - l.trimmed; line >= 0 && line < scrollbackLen {
			l.markers[n] = l.markers[i]
			n++
		}
	}
	l.setMarkersLocked(l.markers[:n])
}

// AdjustForScrollbackTrim moves the list's origin when scrollback lines are
// trimmed from the ring buffer, which lowers every marker's AbsLine by
// linesRemoved. Markers that fall before the new origin are removed. It costs
// nothing when the list is empty, and otherwise only drops what fell off the
// front.
func (l *SemanticMarkerList) AdjustForScrollbackTrim(linesRemoved int) {
	if linesRemoved <= 0 || !l.nonEmpty.Load() {
		// With no markers the origin has nothing to stay consistent with,
		// so it need not move.
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.trimmed += linesRemoved
	drop := 0
	for drop < len(l.markers) && l.markers[drop].AbsLine < l.trimmed {
		drop++
	}
	if drop > 0 {
		l.setMarkersLocked(l.markers[drop:])
	}
}
