package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// panesBorderless reports whether the panes this client is arranging drop their
// own border boxes in favour of one divider drawn between them.
//
// It is the single answer to a question three parts of the app used to work out
// for themselves from config.SharedBorders alone: which border a pane draws,
// whether a separator is painted between two of them, and how many columns the
// layout keeps free for that separator. The setting is only half of it. Shared
// borders merge nothing unless tiling is arranging the panes, and the scrolling
// layout never merges at all, so a pane under any of those keeps its own box.
func (m *OS) panesBorderless() bool {
	return config.SharedBorders && m.AutoTiling && !m.UseScrollingLayout
}

// separatorGap is the columns the layout keeps between two neighbouring panes.
//
// Borderless panes need one: their rectangles are guest output from edge to
// edge, and the divider the user sees is an overlay painted in the column
// between them. Panes drawing their own borders need none. Their two adjacent
// border columns already divide them, so a third column would draw nothing,
// and every pane on that side of every split would be a column narrower for it.
func (m *OS) separatorGap() int {
	if m.panesBorderless() {
		return 1
	}
	return 0
}

// reclaimSeparatorGaps re-lays-out every workspace's panes at the gap the
// layout now reserves, so a column held open for a divider that is no longer
// drawn goes back to the panes beside it instead of sitting empty.
//
// It runs on the toggle itself rather than lazily when a workspace next comes
// into view, for two reasons. Every workspace is still standing in the
// arrangement the tiler gave it at this moment, so redoing its rectangles is
// safe; once tiling is off the user is free to drag those panes about, and
// redoing the layout then would throw that arrangement away. And a workspace
// left holding the gap is the stranding this area has produced before: panes
// drawing their own boxes inside rectangles still spaced for a separator, with
// nobody to settle them but a retile that never runs.
//
// Geometry is applied directly rather than eased into. A pane growing by a
// single column has nothing to travel, and the mode switch it follows is
// structural.
func (m *OS) reclaimSeparatorGaps() {
	if m.UseScrollingLayout {
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
				win := m.getWindowByIntID(intID)
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

// tilablePanes returns the panes a tiler would arrange on a workspace, in the
// order it would arrange them.
func (m *OS) tilablePanes(workspace int) []*terminal.Window {
	var panes []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == workspace && !w.Minimized && !w.Minimizing && !w.IsFloating {
			panes = append(panes, w)
		}
	}
	return panes
}

// placePane puts a pane at a rectangle and gives it the border that rectangle
// was partitioned for.
//
// The border allowance is settled before the resize because it decides how much
// of the rectangle the guest can draw in: announcing the new size first would
// tell the guest a box measured against the border it is about to stop drawing.
// The flag is written directly rather than through SetTiled, which resizes to
// the rectangle the pane has now - one announcement at a size the pane never
// occupied, then a second at the real one. Both are SIGWINCHes and a
// full-screen guest repaints for each.
func (m *OS) placePane(win *terminal.Window, x, y, w, h int, borderless bool) {
	// A snap animation owns its window's geometry until it finishes and would
	// stamp its own rectangle back over this one on the next tick.
	m.CancelSnapAnimation(win)
	win.X, win.Y = x, y
	win.Tiled = borderless
	win.Resize(w, h)
	win.InvalidateCache()
	win.MarkPositionDirty()
}
