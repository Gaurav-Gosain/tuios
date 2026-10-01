package app

import "github.com/Gaurav-Gosain/tuios/internal/terminal"

// setupReflowRemap moves a pane's kitty image placements and text-sizing
// placements with their rows when the pane's emulator reflows on a resize.
// Both record the row they were drawn on, counted from the oldest history
// row, and a reflow moves the text of that row to another index.
func (m *OS) setupReflowRemap(window *terminal.Window) {
	if window == nil || window.Terminal == nil {
		return
	}
	id := window.ID
	kp, ts := m.KittyPassthrough, m.TextSizingState
	window.Terminal.SetReflowFunc(func(remap func(absLine int) int) {
		if kp != nil {
			kp.remapLines(id, remap)
		}
		if ts != nil {
			ts.remapLines(id, remap)
		}
	})
}

// remapLines moves the placements of window id to where remap says their
// rows went.
func (kp *KittyPassthrough) remapLines(id string, remap func(absLine int) int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, p := range kp.placements[id] {
		p.AbsoluteLine = remap(p.AbsoluteLine)
	}
}

// remapLines moves the placements of window id to where remap says their
// rows went.
func (ts *TextSizingState) remapLines(id string, remap func(absLine int) int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, p := range ts.placements[id] {
		p.AbsLine = remap(p.AbsLine)
	}
}
