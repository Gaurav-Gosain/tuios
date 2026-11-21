package app

import (
	"bytes"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

var (
	sixelSweepEvery = 500 * time.Millisecond
	sixelSweepGrace = time.Second
)

var sixelSweepRunning atomic.Bool

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
