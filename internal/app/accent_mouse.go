package app

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

// accentPickerPress routes a left press at dialog-relative (lx, ly). A press on
// the grid or the hue strip also grabs that control, so the colour follows the
// pointer until the button comes up.
func (m *OS) accentPickerPress(lx, ly int) bool {
	hit, ok := m.accentHitAt(lx, ly)
	if !ok {
		return false
	}
	switch hit.Kind {
	case accentHitGrid:
		m.accentDragging, m.accentDrag = true, accentHitGrid
		m.AccentPickerCell(hit.Col, hit.Row)
	case accentHitHue:
		m.accentDragging, m.accentDrag = true, accentHitHue
		m.AccentPickerHueCell(hit.Col)
	case accentHitHex:
		m.AccentPickerFocusHex()
	case accentHitHarmony:
		m.AccentPickerHarmonyAt(hit.Col)
	case accentHitClear:
		m.AccentPickerClear()
	default:
		return false
	}
	return true
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
