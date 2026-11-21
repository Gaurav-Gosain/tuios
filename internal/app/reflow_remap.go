package app

import "github.com/Gaurav-Gosain/tuios/internal/terminal"

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

func (kp *KittyPassthrough) remapLines(id string, remap func(absLine int) int) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	for _, p := range kp.placements[id] {
		p.AbsoluteLine = remap(p.AbsoluteLine)
	}
}

func (ts *TextSizingState) remapLines(id string, remap func(absLine int) int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, p := range ts.placements[id] {
		p.AbsLine = remap(p.AbsLine)
	}
}
