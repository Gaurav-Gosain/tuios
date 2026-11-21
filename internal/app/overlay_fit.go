package app

import "github.com/Gaurav-Gosain/tuios/internal/overlay"

const (
	minPanelRows = 3

	panelChromeRows = 4
)

func (m *OS) panelWidth(preferred int) int {
	return overlay.FitWidth(preferred, m.modalRoomWidth())
}

const modalClearMin = 72

func (m *OS) modalRoomWidth() int {
	rw := m.GetRenderWidth()
	if rw <= 0 {
		return rw
	}
	r := m.viewReserve()
	side := max(r.Left, r.Right)
	if side == 0 {
		return rw
	}
	if room := rw - 2*(side+1); room >= modalClearMin {
		return room
	}
	return rw
}

func (m *OS) panelBodyRows(preferred, extra, width int, tabs []string, hints []overlay.Hint) int {
	rh := m.panelRoomHeight()
	if rh <= 0 {
		return preferred
	}
	return max(min(preferred, rh-panelChrome(extra, width, tabs, hints)), minPanelRows)
}

func panelChrome(extra, width int, tabs []string, hints []overlay.Hint) int {
	chrome := panelChromeRows + extra
	if len(tabs) > 0 {
		chrome += overlay.TabRowCount(tabs, width) + 2
	}
	if len(hints) > 0 {
		chrome += 2 + overlay.HintRowCount(hints, width)
	}
	return chrome
}

func (m *OS) panelBody(preferred, extra, width int, tabs []string, hints []overlay.Hint) (int, []overlay.Hint) {
	hints = m.listHints(hints)
	rows := m.panelBodyRows(preferred, extra, width, tabs, hints)
	rh := m.panelRoomHeight()
	if rh <= 0 || len(hints) == 0 || rows+panelChrome(extra, width, tabs, hints) <= rh {
		return rows, hints
	}
	return m.panelBodyRows(preferred, extra, width, tabs, nil), nil
}

func scrollWindow(scroll, selected, count, visible int) int {
	if count <= visible {
		return 0
	}
	scroll = min(selected, clampInt(scroll, 0, count-visible))
	if selected >= scroll+visible {
		scroll = selected - visible + 1
	}
	return clampInt(scroll, 0, count-visible)
}

func (m *OS) panelRoomHeight() int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return rh
	}
	r := m.viewReserve()
	return max(rh-r.Top-r.Bottom, 1)
}
