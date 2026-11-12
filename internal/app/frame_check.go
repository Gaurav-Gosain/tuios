package app

import (
	"fmt"
	"os"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
)

// The frame check finds what the direct pane and a kept canvas (compose.go)
// miss. With TUIOS_FRAME_CHECK naming a file, every frame that draws a direct
// pane and runs no pass over its layers, kept or not, is also drawn afresh
// into a scratch canvas: cleared to the desktop's ground, every layer drawn in
// depth order, and the pane's cells written right after its own layer. The
// two are compared over the whole screen, the pane's rectangle included, so a
// layer drawn under the pane's cells shows as a miss as well as a cell a kept
// canvas left from an older frame. A difference is written to the file, and
// the e2e harness fails the test that drew it.
//
// It costs a full compose on every such frame, so it is for tests only.

// frameCheckPath is the file the frame check writes to, or "" when it is off.
var frameCheckPath = os.Getenv("TUIOS_FRAME_CHECK")

// frameCheckMax bounds the reports one process writes.
const frameCheckMax = 20

var frameCheckState struct {
	sync.Mutex
	written int
	scratch *frameCanvas
}

// checkFrame draws the frame's layers afresh and reports the first cell where
// the canvas differs. ordered is the frame's layers in depth order, the first
// one the compositor's root.
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
	fresh.ClearTo(desktop)
	area := fresh.Bounds()
	paneDrawn := false
	for _, cl := range ordered {
		if cl.layer == nil || !cl.bounds.Overlaps(area) {
			continue
		}
		m.drawComposedLayer(fresh, cl, painted, grounds, area)
		if !paneDrawn && cl.layer.GetID() == pane.w.ID {
			m.paneCells(fresh, pane, false, nil)
			paneDrawn = true
		}
	}
	if !paneDrawn {
		m.paneCells(fresh, pane, false, nil)
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

// reportFrameMiss appends one miss to the check's file.
func reportFrameMiss(line string) {
	f, err := os.OpenFile(frameCheckPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
