package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) panesBorderless() bool {
	return m.SharedBorders && m.AutoTiling
}

// PanesBorderless is panesBorderless for the input package, whose shared-border
// drag gestures have to agree with the layout about whether there is a shared
// border to drag.
func (m *OS) PanesBorderless() bool { return m.panesBorderless() }

func (m *OS) separatorGap() int {
	if m.panesBorderless() {
		return max(m.PaneGap, 1)
	}
	return m.PaneGap
}

// SeparatorGap is separatorGap for the input package, whose shared-border drag
// gestures have to know which cells between two panes are the divider.
func (m *OS) SeparatorGap() int { return m.separatorGap() }

func (m *OS) reclaimSeparatorGaps() {
	if m.UseScrollingLayout {
		m.scrollingSetPositionsInstant()
		return
	}
	borderless := m.panesBorderless()
	bounds := m.GetBSPBounds()
	for ws := 1; ws <= m.NumWorkspaces; ws++ {
		panes := m.tilablePanes(ws)
		if len(panes) == 0 {
			continue
		}
		if m.UseBSPLayout {
			tree := m.WorkspaceTrees[ws]
			if tree == nil || tree.IsEmpty() {
				continue
			}
			for intID, r := range tree.ApplyLayout(bounds, m.separatorGap()) {
				win := m.GetWindowByIntID(intID)
				if win == nil || win.Workspace != ws {
					continue
				}
				m.placePane(win, r.X, r.Y, r.W, r.H, borderless)
			}
			continue
		}
		for i, l := range m.contentTileLayouts(len(panes)) {
			if i < len(panes) {
				m.placePane(panes[i], l.X, l.Y, l.Width, l.Height, borderless)
			}
		}
	}
}

func (m *OS) tilablePanes(workspace int) []*terminal.Window {
	var panes []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == workspace && !w.Minimized && !w.IsFloating {
			panes = append(panes, w)
		}
	}
	return panes
}

func (m *OS) placePane(win *terminal.Window, x, y, w, h int, borderless bool) {
	m.CancelSnapAnimation(win)
	win.X, win.Y = x, y
	win.Tiled = borderless
	win.Resize(w, h)
	win.InvalidateCache()
	win.MarkPositionDirty()
}
