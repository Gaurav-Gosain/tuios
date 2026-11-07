package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The agents view's layout keys: the width of the list, the side column,
// and the zoom of one region.
//
// The list is 38, 48 or 56 cells by the screen's width unless [agents]
// list_width or the person says otherwise. < and > step it by ckListStep
// cells, and a drag on the gap between the list and the focus sets it; a
// double click there gives the width back to the config. The person's width
// is kept in the client's state file as cells, and every frame clamps it to
// the screen it draws on, so a smaller terminal keeps the saved value for
// the next larger one. w hides the side column and shows it again. Z shows
// the focused region alone over the whole body, and Z or esc gives the
// layout back as it was: the cursor, the scroll, the tab and the draft are
// not touched by a zoom.

const (
	// ckListMin and ckFocusMin are the narrowest the list and the focus
	// get from a width the person or the config set.
	ckListMin  = 28
	ckFocusMin = 40
	// ckListStep is how far < and > move the edge.
	ckListStep = 4
)

// ckDivider is the edge between the list and the focus: where the frame drew
// it, whether the pointer is on it, and a drag under way.
type ckDivider struct {
	// x0, x1, y0, y1 are its cells on screen, empty when not drawn.
	x0, x1, y0, y1 int
	hover, drag    bool
	lastClick      time.Time
}

func (d *ckDivider) has(x, y int) bool {
	return x >= d.x0 && x < d.x1 && y >= d.y0 && y < d.y1
}

// ckDefaultFleetWidth is the list's width that follows the screen.
func ckDefaultFleetWidth(w int) int {
	switch {
	case w < ckOneColumn:
		return w
	case w < ckTwoLine:
		return 38
	case w < ckWide:
		return 48
	}
	return 56
}

// cockpitFleetWidth is the list's width on a view w cells wide: the
// person's, else the config's, else the default, clamped so the list keeps
// ckListMin and the focus ckFocusMin. Under ckOneColumn the list takes the
// whole width, as before.
func (m *OS) cockpitFleetWidth(w int) int {
	def := ckDefaultFleetWidth(w)
	if w < ckOneColumn {
		return def
	}
	want := m.Inbox.ck.listWidth
	if want <= 0 && m.UserConfig != nil {
		want = m.UserConfig.Agents.ListWidthCells(w)
	}
	if want <= 0 {
		return def
	}
	return ckClampList(want, w)
}

// ckClampList keeps a list width between ckListMin and the width that leaves
// the focus ckFocusMin, on a view w cells wide.
func ckClampList(want, w int) int {
	return min(max(want, ckListMin), max(w-5-ckFocusMin, ckListMin))
}

// cockpitSideShown reports whether the side column fits beside a focus
// focusW cells wide: the screen is wide enough, the person did not hide it,
// and the focus keeps ckFocusMin beside it. A wide list hides the side
// column before it squeezes the focus.
func (m *OS) cockpitSideShown(w, focusW int) bool {
	return w >= ckWide && !m.Inbox.ck.sideHidden && focusW-ckSideWidth-1 >= ckFocusMin
}

// CockpitListWider moves the list's edge by d cells and keeps the width.
func (m *OS) CockpitListWider(d int) {
	w := m.GetRenderWidth()
	if w < ckOneColumn {
		return
	}
	ck := &m.Inbox.ck
	ck.listWidth = ckClampList(m.cockpitFleetWidth(w)+d, w)
	m.cockpitTouch()
	m.saveSidebarState()
}

// CockpitToggleSide hides the side column, or shows it again.
func (m *OS) CockpitToggleSide() {
	ck := &m.Inbox.ck
	ck.sideHidden = !ck.sideHidden
	m.cockpitTouch()
	m.saveSidebarState()
}

// CockpitToggleZoom shows the focused region alone, or the whole view again.
func (m *OS) CockpitToggleZoom() {
	ck := &m.Inbox.ck
	ck.zoom = !ck.zoom
	m.cockpitTouch()
}

// cockpitDividerPress takes a press on the edge of the list: it starts a
// drag, and a second press within ckDoubleClick gives the width back to
// the config. It reports whether the press was on the edge.
func (m *OS) cockpitDividerPress(x, y int) bool {
	ck := &m.Inbox.ck
	if !ck.div.has(x, y) {
		return false
	}
	now := time.Now()
	if now.Sub(ck.div.lastClick) <= ckDoubleClick {
		ck.div.lastClick = time.Time{}
		ck.div.drag = false
		ck.listWidth = 0
		m.cockpitTouch()
		m.saveSidebarState()
		return true
	}
	ck.div.lastClick = now
	ck.div.drag = true
	m.cockpitTouch()
	return true
}

// cockpitDividerMotion follows the pointer while the edge is held, and
// lights the edge while the pointer is on it. It reports whether the motion
// was the edge's.
func (m *OS) cockpitDividerMotion(x, y int) bool {
	ck := &m.Inbox.ck
	if ck.div.drag {
		w := m.GetRenderWidth()
		// The edge sits one cell right of the list.
		if want := ckClampList(x-1, w); want != m.cockpitFleetWidth(w) || ck.listWidth == 0 {
			ck.listWidth = want
			m.cockpitTouch()
		}
		return true
	}
	on := ck.div.has(x, y)
	if on != ck.div.hover {
		ck.div.hover = on
		if on {
			m.SetPointerShape(PointerEWResize)
		} else {
			m.ResetPointerShape()
		}
		m.cockpitTouch()
	}
	return on
}

// cockpitDividerRelease ends a drag of the edge and keeps the width. It
// reports whether a drag was under way.
func (m *OS) cockpitDividerRelease() bool {
	ck := &m.Inbox.ck
	if !ck.div.drag {
		return false
	}
	ck.div.drag = false
	m.cockpitTouch()
	m.saveSidebarState()
	return true
}

// drawCockpitDivider draws the edge of the list while the pointer is on it
// or drags it: a bar, so it shows at 16 colours too. It records where the
// edge is for the next press.
func (m *OS) drawCockpitDivider(c *ckCanvas, l ckLook, fleetW, y0, y1, top int) {
	ck := &m.Inbox.ck
	x := fleetW + 1
	ck.div.x0, ck.div.x1, ck.div.y0, ck.div.y1 = x-1, x+2, top+y0, top+y1
	if !ck.div.hover && !ck.div.drag {
		return
	}
	for y := y0; y < y1; y++ {
		c.text(x, y, x+1, ckSeg{t: "┃", fg: l.pal.Accent})
	}
}

// cockpitLayoutKey runs the layout keys. handled is false for every other
// action.
func (m *OS) cockpitLayoutKey(action string) (tea.Cmd, bool) {
	switch action {
	case config.ActionInboxListNarrower:
		m.CockpitListWider(-ckListStep)
	case config.ActionInboxListWider:
		m.CockpitListWider(ckListStep)
	case config.ActionInboxSide:
		m.CockpitToggleSide()
	case config.ActionInboxZoom:
		m.CockpitToggleZoom()
	default:
		return nil, false
	}
	return nil, true
}

// cockpitLayoutHints are the layout hints for the key bar: the edge's drag
// while the pointer is on it. A zoom's way back is esc's hint.
func (m *OS) cockpitLayoutHints() []overlay.Hint {
	if d := &m.Inbox.ck.div; d.hover || d.drag {
		return []overlay.Hint{{Key: "drag", Label: "to resize", Priority: overlay.HintEssential}}
	}
	return nil
}
