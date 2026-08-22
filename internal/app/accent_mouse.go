package app

import tea "charm.land/bubbletea/v2"

// The picker's mouse routing. Every decision here is taken against the rects
// the renderer recorded as it drew (m.accentHits), never against the layout
// arithmetic that produced them: the grid reflows with the dialog's width and
// the screen's height, and a handler doing its own sums would eventually
// disagree with the cell the user is looking at.

// accentHitAt returns the recorded cell containing dialog-relative (lx, ly).
func (m *OS) accentHitAt(lx, ly int) (accentHit, bool) {
	for _, h := range m.accentHits {
		if h.Rect.Contains(lx, ly) {
			return h, true
		}
	}
	return accentHit{}, false
}

// accentHitByKindCol returns the recorded rect for one indexed control, which
// is how a grabbed slider finds its own track again after the pointer has left
// it: the answer is the rect that was drawn, not one worked out here.
func (m *OS) accentHitByKindCol(kind accentHitKind, col int) (accentHit, bool) {
	for _, h := range m.accentHits {
		if h.Kind == kind && h.Col == col {
			return h, true
		}
	}
	return accentHit{}, false
}

// accentPickerPress routes a left press at dialog-relative (lx, ly). A press on
// the grid or the hue strip also grabs that control, so the colour follows the
// pointer until the button comes up. The clear control can reach the daemon, so
// the press hands back a command the way the keyboard path does.
func (m *OS) accentPickerPress(lx, ly int) (bool, tea.Cmd) {
	hit, ok := m.accentHitAt(lx, ly)
	if !ok {
		return false, nil
	}
	switch hit.Kind {
	case accentHitGrid:
		m.accentDragging, m.accentDrag = true, accentHitGrid
		m.AccentPickerCell(hit.Col, hit.Row)
	case accentHitHue:
		m.accentDragging, m.accentDrag = true, accentHitHue
		m.AccentPickerHueCell(hit.Col)
	case accentHitSlider:
		m.accentDragging, m.accentDrag, m.accentDragCol = true, accentHitSlider, hit.Col
		m.AccentPickerSliderAt(accentChannel(hit.Col), lx-hit.Rect.X0, hit.Rect.X1-hit.Rect.X0)
	case accentHitANSI:
		m.AccentPickerSlot(hit.Col)
	case accentHitNamed:
		m.AccentPickerNamed(hit.Col)
	case accentHitHex:
		m.AccentPickerFocusHex()
	case accentHitHarmony:
		m.AccentPickerHarmonyAt(hit.Col)
	case accentHitClear:
		return true, m.AccentPickerClear()
	case accentHitHint:
		return m.accentPickerHintPress(hit.Col)
	default:
		return false, nil
	}
	return true, nil
}

// accentPickerHintPress runs the hint in the bottom border that was clicked.
// Applying and cancelling are keyboard-only otherwise, which leaves a mouse
// user having to reach for the keyboard to finish a gesture they started with
// the pointer.
func (m *OS) accentPickerHintPress(i int) (bool, tea.Cmd) {
	switch i {
	case accentHintFocus:
		m.AccentPickerFocus(1)
	case accentHintApply:
		return true, m.AccentPickerApply()
	case accentHintClear:
		return true, m.AccentPickerClear()
	case accentHintCancel:
		m.CloseAccentPicker()
	default:
		return false, nil
	}
	return true, nil
}

// accentPickerDragTo continues a grabbed drag at dialog-relative (lx, ly).
//
// The drag stays on the control it started on. Sliding along the hue strip
// wanders a row into the grid on the way, and a drag that changed meaning
// halfway would repaint the swatch with whatever the pointer brushed past.
func (m *OS) accentPickerDragTo(lx, ly int) {
	// The host reports every motion, held or not, so the grab flag alone does not
	// mean a drag is under way: a release lost when the pointer leaves the
	// surface would leave it set and the colour would track bare hover from then
	// on. The button itself is the authority on whether this is still a drag.
	if !m.accentDragging || !m.pointerDown {
		return
	}
	// A grabbed slider keeps tracking the pointer's column after it has left the
	// track, which is what a user dragging a thumb to one end and overshooting
	// expects. The other controls are two-dimensional and have no such reading of
	// a point outside them.
	if m.accentDrag == accentHitSlider {
		h, ok := m.accentHitByKindCol(accentHitSlider, m.accentDragCol)
		if !ok {
			return
		}
		m.AccentPickerSliderAt(accentChannel(h.Col), lx-h.Rect.X0, h.Rect.X1-h.Rect.X0)
		return
	}
	hit, ok := m.accentHitAt(lx, ly)
	if !ok || hit.Kind != m.accentDrag {
		return
	}
	switch hit.Kind {
	case accentHitGrid:
		m.AccentPickerCell(hit.Col, hit.Row)
	case accentHitHue:
		m.AccentPickerHueCell(hit.Col)
	}
}
