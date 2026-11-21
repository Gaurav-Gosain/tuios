package app

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	ckListMin  = 28
	ckFocusMin = 40
	ckListStep = 4
)

type ckDivider struct {
	x0, x1, y0, y1 int
	hover, drag    bool
	lastClick      time.Time
}

func (d *ckDivider) has(x, y int) bool {
	return x >= d.x0 && x < d.x1 && y >= d.y0 && y < d.y1
}

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

func ckClampList(want, w int) int {
	return min(max(want, ckListMin), max(w-5-ckFocusMin, ckListMin))
}

func (m *OS) cockpitSideShown(w, focusW int) bool {
	return cockpitSideRoom(w, focusW) && !m.Inbox.ck.sideHidden
}

func cockpitSideRoom(w, focusW int) bool {
	return w >= ckWide && focusW-ckSideWidth-1 >= ckFocusMin
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

// CockpitToggleSide hides the side column, or shows it again. With no room
// for the column the key changes nothing and says why.
func (m *OS) CockpitToggleSide() {
	ck := &m.Inbox.ck
	if !ck.sideRoom {
		ck.layoutRefused = config.ActionInboxSide
		m.cockpitTouch()
		return
	}
	ck.sideHidden = !ck.sideHidden
	m.cockpitTouch()
	m.saveSidebarState()
}

// CockpitToggleZoom shows the focused region alone, or the whole view again.
// On a plan with the keys in the list, the zoom is the focus, where the card
// has every row for the plan. Under ckOneColumn the key changes nothing and
// says why, unless a selected plan's card is drawn, which reads the zoom.
func (m *OS) CockpitToggleZoom() {
	ck := &m.Inbox.ck
	if ck.zoom {
		m.cockpitUnzoom()
		return
	}
	if m.cockpitZoomRefused() {
		ck.layoutRefused = config.ActionInboxZoom
		m.cockpitTouch()
		return
	}
	ck.zoom = true
	if m.CockpitRegion() == ckRegionList && m.inboxPlanSelected() && ck.regionBox[ckRegionFocus].shown() {
		ck.region, ck.zoomTook = ckRegionFocus, true
	}
	m.cockpitTouch()
}

func (m *OS) cockpitUnzoom() {
	ck := &m.Inbox.ck
	if ck.zoomTook && ck.region == ckRegionFocus {
		ck.region = ckRegionList
	}
	ck.zoom, ck.zoomTook = false, false
	m.cockpitTouch()
}

func (m *OS) cockpitDividerPress(x, y int) bool {
	ck := &m.Inbox.ck
	if !ck.div.has(x, y) {
		return false
	}
	now := time.Now()
	if now.Sub(ck.div.lastClick) <= m.DoubleClickWindow() {
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

func (m *OS) cockpitDividerMotion(x, y int) bool {
	ck := &m.Inbox.ck
	if ck.div.drag {
		w := m.GetRenderWidth()
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

func (m *OS) cockpitLayoutHints() []overlay.Hint {
	if d := &m.Inbox.ck.div; d.hover || d.drag {
		return []overlay.Hint{{Key: "drag", Label: "to resize", Priority: overlay.HintEssential}}
	}
	return nil
}

func (m *OS) cockpitZoomRefused() bool {
	return m.GetRenderWidth() < ckOneColumn && !(m.inboxPlanSelected() && m.Inbox.ck.focusShown)
}

func (m *OS) cockpitLayoutNote() overlay.Hint {
	ck := &m.Inbox.ck
	switch {
	case ck.layoutRefused == config.ActionInboxSide && !ck.sideRoom:
		label := "the side column needs a narrower list"
		if m.GetRenderWidth() < ckWide {
			label = "the side column needs " + strconv.Itoa(ckWide) + " columns"
		}
		return overlay.Hint{Key: m.inboxKey(config.ActionInboxSide), Label: label, Priority: overlay.HintEssential}
	case ck.layoutRefused == config.ActionInboxZoom && m.cockpitZoomRefused():
		return overlay.Hint{Key: m.inboxKey(config.ActionInboxZoom), Label: "zoom needs " + strconv.Itoa(ckOneColumn) + " columns", Priority: overlay.HintEssential}
	}
	return overlay.Hint{}
}

func (m *OS) clearLayoutRefusal() {
	if ck := &m.Inbox.ck; ck.layoutRefused != "" {
		ck.layoutRefused = ""
		m.cockpitTouch()
	}
}
