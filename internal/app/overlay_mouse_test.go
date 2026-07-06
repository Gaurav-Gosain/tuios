package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// renderSettingsHit renders the settings panel and records its hit geometry the
// way renderOverlays would, so the mouse routing can be exercised in a test.
func (m *OS) renderSettingsHit() {
	m.reconcileOverlayZOrder()
	content, geo, rows := m.renderSettings()
	_ = content
	x, y := m.overlayOrigin("settings", geo)
	m.OverlayHits = []overlayPanelHit{{Kind: "settings", OriginX: x, OriginY: y, Z: m.overlayZ("settings"), Geo: geo, Rows: rows}}
}

func (m *OS) settingsHit() overlayPanelHit { return m.OverlayHits[0] }

func TestOverlayClickSelectsTabAndRow(t *testing.T) {
	m := &OS{}
	m.Width, m.Height = 120, 40
	m.ShowSettings = true
	m.renderSettingsHit()

	// Click the second tab (Dock) and confirm the category switches.
	h := m.settingsHit()
	if len(h.Geo.Tabs) < 2 {
		t.Fatal("expected settings tabs")
	}
	tab := h.Geo.Tabs[1]
	tx := h.OriginX + (tab.X0+tab.X1)/2
	ty := h.OriginY + tab.Y0
	handled, _ := m.OverlayMouseClick(tx, ty, false)
	if !handled || m.SettingsCategory != 1 {
		t.Fatalf("tab click: handled=%v category=%d want 1", handled, m.SettingsCategory)
	}

	// Re-render for the new category, then click the third row to select it.
	m.renderSettingsHit()
	h = m.settingsHit()
	if len(h.Rows) < 3 {
		t.Fatal("expected settings rows")
	}
	row := h.Rows[2]
	rx := h.OriginX + 4
	ry := h.OriginY + row.Rect.Y0
	if handled, _ := m.OverlayMouseClick(rx, ry, false); !handled || m.SettingsSelected != 2 {
		t.Fatalf("row click: handled=%v selected=%d want 2", handled, m.SettingsSelected)
	}
}

func TestOverlayClickAwayCloses(t *testing.T) {
	m := &OS{}
	m.Width, m.Height = 120, 40
	m.ShowSettings = true
	m.renderSettingsHit()

	// A click at the top-left corner is outside the centered panel and dismisses.
	handled, _ := m.OverlayMouseClick(0, 0, false)
	if !handled || m.ShowSettings {
		t.Fatalf("click-away: handled=%v ShowSettings=%v want dismissed", handled, m.ShowSettings)
	}
}

func TestOverlayLeftDragOnChromeMoves(t *testing.T) {
	m := &OS{}
	m.Width, m.Height = 120, 40
	m.ShowSettings = true
	m.renderSettingsHit()
	h := m.settingsHit()

	// A left-click on the title row (chrome, not a tab or setting row) grabs
	// the panel for dragging.
	lx, ly := 4, h.Geo.TitleBar.Y0
	if handled, _ := m.OverlayMouseClick(h.OriginX+lx, h.OriginY+ly, false); !handled || !m.OverlayDrag.Active {
		t.Fatal("left-click on the title should start a drag")
	}
	if m.OverlayDrag.Kind != "settings" {
		t.Fatalf("drag kind=%q want settings", m.OverlayDrag.Kind)
	}
	if !m.OverlayMouseMotion(h.OriginX+lx+5, h.OriginY+ly+4) {
		t.Fatal("motion should be consumed during drag")
	}
	if off := m.overlayOffset("settings"); off[0] != 5 || off[1] != 4 {
		t.Errorf("settings offset=%v want [5 4]", off)
	}
	m.OverlayMouseRelease()
}

func TestOverlayRightDragMoves(t *testing.T) {
	m := &OS{}
	m.Width, m.Height = 120, 40
	m.ShowSettings = true
	m.renderSettingsHit()
	h := m.settingsHit()

	if handled, _ := m.OverlayMouseClick(h.OriginX+5, h.OriginY+5, true); !handled || !m.OverlayDrag.Active {
		t.Fatal("right-click should start a drag")
	}
	if !m.OverlayMouseMotion(h.OriginX+5+7, h.OriginY+5+3) {
		t.Fatal("motion should be consumed during drag")
	}
	if off := m.overlayOffset("settings"); off[0] != 7 || off[1] != 3 {
		t.Errorf("settings offset=%v want [7 3]", off)
	}
	m.OverlayMouseRelease()
	if m.OverlayDrag.Active {
		t.Error("release should end the drag")
	}
}

// TestOverlayIndependentPanels checks that two stacked overlay panels are hit
// and dragged independently: a click on each routes to that panel, and dragging
// one does not move the other.
func TestOverlayIndependentPanels(t *testing.T) {
	m := &OS{}
	m.Width, m.Height = 160, 48

	// Two panels: "settings" on the left (bottom of stack), "themepicker" on the
	// right (top of stack).
	m.OverlayZOrder = []string{"settings", "themepicker"}
	settings := overlayPanelHit{Kind: "settings", OriginX: 10, OriginY: 10, Z: m.overlayZ("settings"), Geo: overlay.Geometry{Width: 40, Height: 12}}
	picker := overlayPanelHit{Kind: "themepicker", OriginX: 80, OriginY: 14, Z: m.overlayZ("themepicker"), Geo: overlay.Geometry{Width: 40, Height: 12}}
	m.OverlayHits = []overlayPanelHit{settings, picker}

	// A click inside the picker's rect grabs the picker, not settings.
	if handled, _ := m.OverlayMouseClick(85, 15, true); !handled || m.OverlayDrag.Kind != "themepicker" {
		t.Fatalf("click in picker should drag picker, got kind=%q", m.OverlayDrag.Kind)
	}
	m.OverlayMouseMotion(90, 18)
	if off := m.overlayOffset("themepicker"); off == ([2]int{}) {
		t.Error("themepicker offset should have changed")
	}
	if off := m.overlayOffset("settings"); off != ([2]int{}) {
		t.Errorf("settings offset should be unchanged, got %v", off)
	}
	m.OverlayMouseRelease()

	// Clicking the settings panel raises it to the top of the stack.
	m.OverlayMouseClick(15, 12, false)
	if top := m.topmostOverlayKind(); top != "settings" {
		t.Errorf("after clicking settings, topmost=%q want settings", top)
	}
}
