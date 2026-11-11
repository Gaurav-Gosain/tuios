package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// DoubleClickWindow is how long after a click a second one still counts as a
// double click (mouse.double_click_ms). Every double-click detector reads it:
// the pane's text selection, the rail divider, the Agents list and the draft
// editor.
func (m *OS) DoubleClickWindow() time.Duration {
	ms := m.Settings.MouseDoubleClickMS
	if ms <= 0 {
		ms = config.MouseDoubleClickDefault
	}
	return time.Duration(ms) * time.Millisecond
}

// DragThreshold is the summed cell movement that makes a press a drag and not
// a click (mouse.drag_threshold).
func (m *OS) DragThreshold() int {
	if n := m.Settings.MouseDragThreshold; n > 0 {
		return n
	}
	return config.MouseDragThresholdDefault
}
