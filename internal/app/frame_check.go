package app

import (
	"fmt"
	"os"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
)

var frameCheckPath = os.Getenv("TUIOS_FRAME_CHECK")

const frameCheckMax = 20

var frameCheckState struct {
	sync.Mutex
	written int
	scratch *frameCanvas
}

func (m *OS) checkFrame(canvas *frameCanvas, ordered []composedLayer, painted bool, grounds *frameGrounds, desktop ground, pane *directPane, kept bool) {
	if frameCheckPath == "" || pane == nil {
		return
	}
	s := &frameCheckState
	s.Lock()
	defer s.Unlock()
	if s.written >= frameCheckMax {
		return
	}
	if s.scratch == nil || s.scratch.Width() != canvas.Width() || s.scratch.Height() != canvas.Height() {
		s.scratch = &frameCanvas{Buffer: *uv.NewBuffer(canvas.Width(), canvas.Height())}
	}
	fresh := s.scratch
	held := m.heldSource(canvas, kept)
	fresh.ClearTo(desktop)
	area := fresh.Bounds()
	paneDrawn := false
	for _, cl := range ordered {
		if cl.layer == nil || !cl.bounds.Overlaps(area) {
			continue
		}
		m.drawComposedLayer(fresh, cl, painted, grounds, area)
		if !paneDrawn && cl.layer.GetID() == pane.w.ID {
			m.paneCells(fresh, pane, false, nil, held)
			paneDrawn = true
		}
	}
	if !paneDrawn {
		m.paneCells(fresh, pane, false, nil, held)
	}
	for y := range canvas.Lines {
		for x := range canvas.Lines[y] {
			a, b := &canvas.Lines[y][x], &fresh.Lines[y][x]
			if !a.Equal(b) {
				s.written++
				reportFrameMiss(fmt.Sprintf("frame differs from a fresh compose at cell %d,%d (kept %v): drawn %q %+v, fresh %q %+v (%d layers)\n",
					x, y, kept, a.Content, a.Style, b.Content, b.Style, len(ordered)-1))
				return
			}
		}
	}
}

func reportFrameMiss(line string) {
	f, err := os.OpenFile(frameCheckPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
