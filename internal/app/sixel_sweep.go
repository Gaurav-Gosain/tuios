package app

import (
	"bytes"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Freeing images nothing shows any more.
//
// An image is held while any of its cells may still be drawn: on the screen,
// on the main screen under an alternate one, or in the scrollback. A program
// that animates redraws its picture in the same cells many times a second,
// each frame a new image over the last, and every one it replaced has no cells
// left at all. Waiting for the per-pane budget to evict those kept a pane's
// worth of dead frames resident for good.
//
// So the pane is swept: its grids and scrollback are read for the image ids
// still named, and every other image of that pane is freed. It runs off the
// UI goroutine, at most every sixelSweepEvery, and only while the pane holds
// an image the last frame did not show.

// Variables so a test can shorten them.
var (
	sixelSweepEvery = 500 * time.Millisecond
	// sixelSweepGrace is how long a new image is left alone: it is registered
	// just before its cells are written.
	sixelSweepGrace = time.Second
)

// sixelSweepRunning keeps one sweep at a time per process.
var sixelSweepRunning atomic.Bool

// sweepSixelImages starts a sweep of the panes that hold images the last
// frame did not show, when one is due.
func (m *OS) sweepSixelImages() {
	sp := m.SixelPassthrough
	if sp == nil {
		return
	}
	now := time.Now()
	sp.mu.Lock()
	if now.Sub(sp.lastSweep) < sixelSweepEvery {
		sp.mu.Unlock()
		return
	}
	hidden := map[string]bool{}
	for id, e := range sp.images {
		if !sp.frame.visible[id] && now.Sub(e.born) > sixelSweepGrace {
			hidden[e.windowID] = true
		}
	}
	if len(hidden) == 0 {
		sp.mu.Unlock()
		return
	}
	sp.lastSweep = now
	sp.mu.Unlock()

	var wins []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && hidden[w.ID] && w.Terminal != nil {
			wins = append(wins, w)
		}
	}
	if len(wins) == 0 || !sixelSweepRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer sixelSweepRunning.Store(false)
		for _, w := range wins {
			sp.keepOnly(w.ID, sixelRefs(w), now)
		}
	}()
}

// sixelRefs is the set of image ids a window's cells still name.
func sixelRefs(w *terminal.Window) map[uint32]bool {
	w.RLockIO()
	defer w.RUnlockIO()
	term := w.Terminal
	refs := map[uint32]bool{}
	note := func(content string) {
		if id, _, _, ok := vt.ParseSixelMarker(content); ok {
			refs[id] = true
		}
	}
	width, height := term.Width(), term.Height()
	alt := term.IsAltScreen()
	for y := range height {
		for x := range width {
			if c := term.CellAt(x, y); c != nil {
				note(c.Content)
			}
			if alt {
				if c := term.MainCellAt(x, y); c != nil {
					note(c.Content)
				}
			}
		}
	}
	// The history is read as text cells, which builds no cell and allocates
	// nothing for a cell that is not a marker.
	lead := []byte(vt.SixelMarkerLead)
	term.ScrollbackText(0, term.ScrollbackLen(), func(_, _ int, cells []vt.TextCell) bool {
		for _, c := range cells {
			if bytes.HasPrefix(c.Content, lead) {
				note(string(c.Content))
			}
		}
		return true
	})
	return refs
}

// keepOnly frees every image of windowID that refs does not name, except
// those shown on the last frame and those younger than the grace period at
// the sweep's start.
func (sp *SixelPassthrough) keepOnly(windowID string, refs map[uint32]bool, started time.Time) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for id, e := range sp.images {
		if e.windowID != windowID || refs[id] || sp.frame.visible[id] || started.Sub(e.born) <= sixelSweepGrace {
			continue
		}
		sp.dropLocked(id)
	}
}
