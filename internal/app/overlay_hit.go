package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// overlayRowHit is a single interactive body row of an overlay panel, in
// panel-relative coordinates. Dec/Inc mark the left/right control hot-zones
// (cycler arrows or a toggle) when the row has an adjustable value.
type overlayRowHit struct {
	Rect overlay.Rect
	Idx  int
	Dec  overlay.Rect
	Inc  overlay.Rect
}

// overlayPanelHit records the on-screen geometry of one overlay panel so mouse
// events can be routed without re-deriving layout. One is appended to
// OverlayHits per panel each frame by placeOverlayPanel.
type overlayPanelHit struct {
	Kind    string // "settings", "help", "palette", "themepicker"; "" when none
	OriginX int
	OriginY int
	Z       int
	Geo     overlay.Geometry
	Rows    []overlayRowHit
}

// overlayKindOrder is the deterministic order newly-opened overlays are added to
// the stack (used only to break ties when several open in the same frame).
var overlayKindOrder = []string{"help", "palette", "session", "layout", "aggregate", "settings", "themepicker"}

// openOverlayKinds returns the set of draggable overlay kinds currently shown.
func (m *OS) openOverlayKinds() map[string]bool {
	open := map[string]bool{}
	if m.ShowHelp {
		open["help"] = true
	}
	if m.ShowCommandPalette {
		open["palette"] = true
	}
	if m.ShowSessionSwitcher {
		open["session"] = true
	}
	if m.ShowLayoutPicker {
		open["layout"] = true
	}
	if m.ShowAggregateView {
		open["aggregate"] = true
	}
	if m.ShowSettings {
		open["settings"] = true
	}
	if m.ShowThemePicker {
		open["themepicker"] = true
	}
	return open
}

// reconcileOverlayZOrder drops closed overlays from the stacking order and
// appends newly-opened ones on top, preserving the order of ones already open.
func (m *OS) reconcileOverlayZOrder() {
	open := m.openOverlayKinds()
	kept := m.OverlayZOrder[:0]
	for _, k := range m.OverlayZOrder {
		if open[k] {
			kept = append(kept, k)
			delete(open, k)
		}
	}
	m.OverlayZOrder = kept
	for _, k := range overlayKindOrder {
		if open[k] {
			m.OverlayZOrder = append(m.OverlayZOrder, k)
		}
	}
}

// overlayZ returns the z-index for an overlay kind from its position in the
// stacking order.
func (m *OS) overlayZ(kind string) int {
	for i, k := range m.OverlayZOrder {
		if k == kind {
			return config.ZIndexOverlayBase + i
		}
	}
	return config.ZIndexOverlayBase
}

// raiseOverlay moves a kind to the top of the stacking order.
func (m *OS) raiseOverlay(kind string) {
	idx := -1
	for i, k := range m.OverlayZOrder {
		if k == kind {
			idx = i
			break
		}
	}
	if idx < 0 || idx == len(m.OverlayZOrder)-1 {
		return // not open or already on top
	}
	m.OverlayZOrder = append(m.OverlayZOrder[:idx], m.OverlayZOrder[idx+1:]...)
	m.OverlayZOrder = append(m.OverlayZOrder, kind)
}

// overlayDragState tracks an in-progress overlay move.
type overlayDragState struct {
	Active  bool
	Kind    string // which overlay panel is being dragged
	OffsetX int    // cursor offset within the panel at grab time
	OffsetY int
}

// overlayOffset returns the drag displacement for an overlay kind (zero when
// unset, i.e. centered).
func (m *OS) overlayOffset(kind string) [2]int {
	if m.OverlayOffsets == nil {
		return [2]int{}
	}
	return m.OverlayOffsets[kind]
}

// setOverlayOffset stores the drag displacement for an overlay kind.
func (m *OS) setOverlayOffset(kind string, x, y int) {
	if m.OverlayOffsets == nil {
		m.OverlayOffsets = make(map[string][2]int)
	}
	m.OverlayOffsets[kind] = [2]int{x, y}
}

// overlayOrigin returns the top-left screen cell for an overlay panel: centered,
// shifted by that kind's drag offset, and clamped so the panel stays on screen.
func (m *OS) overlayOrigin(kind string, geo overlay.Geometry) (int, int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	off := m.overlayOffset(kind)
	x := (rw-geo.Width)/2 + off[0]
	y := (rh-geo.Height)/2 + off[1]
	x = max(min(x, rw-geo.Width), 0)
	y = max(min(y, rh-geo.Height), 0)
	return x, y
}

// placeOverlayPanel positions a content-sized overlay panel as a layer
// (centered + that kind's drag offset), records its hit geometry, and appends
// it. Using a content-sized layer instead of a full-screen lipgloss.Place keeps
// the windows behind it visible. A full-screen Place fills the surrounding area
// with opaque spaces that blank the desktop.
func (m *OS) placeOverlayPanel(layers []*lipgloss.Layer, kind, content string, geo overlay.Geometry, rows []overlayRowHit) []*lipgloss.Layer {
	x, y := m.overlayOrigin(kind, geo)
	z := m.overlayZ(kind)
	m.OverlayHits = append(m.OverlayHits, overlayPanelHit{Kind: kind, OriginX: x, OriginY: y, Z: z, Geo: geo, Rows: rows})
	return append(layers, lipgloss.NewLayer(content).X(x).Y(y).Z(z).ID(kind))
}

// centeredBoxLayer centers a content-sized box on screen as a layer. Unlike a
// full-screen lipgloss.Place, this leaves the windows around the box visible
// instead of blanking them with opaque padding spaces.
func (m *OS) centeredBoxLayer(box string, z int, id string) *lipgloss.Layer {
	w := lipgloss.Width(box)
	h := lipgloss.Height(box)
	x := max((m.GetRenderWidth()-w)/2, 0)
	y := max((m.GetRenderHeight()-h)/2, 0)
	return lipgloss.NewLayer(box).X(x).Y(y).Z(z).ID(id)
}

// syncOverlayASCII mirrors the ASCII-only setting into the overlay package,
// which has no dependency on tuios config.
func syncOverlayASCII() {
	overlay.ASCII = config.UseASCIIOnly
}
