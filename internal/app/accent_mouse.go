package app

import tea "charm.land/bubbletea/v2"

func (m *OS) accentHitAt(lx, ly int) (accentHit, bool) {
	for _, h := range m.accentHits {
		if h.Rect.Contains(lx, ly) {
			return h, true
		}
	}
	return accentHit{}, false
}

func (m *OS) accentHitByKindCol(kind accentHitKind, col int) (accentHit, bool) {
	for _, h := range m.accentHits {
		if h.Kind == kind && h.Col == col {
			return h, true
		}
	}
	return accentHit{}, false
}

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

func (m *OS) accentPickerDragTo(lx, ly int) {
	if !m.accentDragging || !m.pointerDown {
		return
	}
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
