package app

import (
	"fmt"
	"image"
	"os"
	"slices"
	"sync"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// The frame check finds what a kept canvas (compose.go) misses. With
// TUIOS_FRAME_CHECK naming a file, every frame that keeps the canvas is also
// drawn afresh from its layers into a scratch canvas, and the two are
// compared outside the direct pane's content, which both copy from the
// emulator. A difference is a write to the canvas the keep rule does not
// know of: the person would have been shown cells left from an older frame.
// It is written to the file, and the e2e harness fails the test that drew it.
//
// It costs a full compose on every kept frame, so it is for tests only.

// frameCheckPath is the file the frame check writes to, or "" when it is off.
var frameCheckPath = os.Getenv("TUIOS_FRAME_CHECK")

// frameCheckMax bounds the reports one process writes.
const frameCheckMax = 20

var frameCheckState struct {
	sync.Mutex
	written int
	scratch *frameCanvas
}

// checkKeptFrame draws the frame's layers afresh and reports the first cell
// where the kept canvas differs. A kept frame has no pass over its layers and
// every layer is drawn from its cells, so drawing it is clearing the canvas to
// the desktop's ground and copying each layer's cells in z order.
func (m *OS) checkKeptFrame(canvas *frameCanvas, layers []*lipgloss.Layer, paneLayers int, desktop ground) {
	if frameCheckPath == "" {
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
	ordered := slices.Clone(layers)
	ordered = slices.DeleteFunc(ordered, func(l *lipgloss.Layer) bool { return l == nil })
	slices.SortStableFunc(ordered, func(a, b *lipgloss.Layer) int { return a.GetZ() - b.GetZ() })
	for _, l := range ordered {
		if cl := m.layerCells[l.GetID()]; cl != nil {
			cl.blit(fresh, l.GetX(), l.GetY())
		}
	}
	var pane image.Rectangle
	if h := &m.directHold; h.drawn {
		pane = h.rect
	}
	for y := range canvas.Lines {
		for x := range canvas.Lines[y] {
			if image.Pt(x, y).In(pane) {
				continue
			}
			a, b := &canvas.Lines[y][x], &fresh.Lines[y][x]
			if !a.Equal(b) {
				s.written++
				reportFrameMiss(fmt.Sprintf("kept canvas differs from a fresh compose at cell %d,%d: kept %q %+v, fresh %q %+v (%d layers, %d of them panes)\n",
					x, y, a.Content, a.Style, b.Content, b.Style, len(ordered), paneLayers))
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
