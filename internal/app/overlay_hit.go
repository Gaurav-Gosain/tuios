package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"slices"
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
	Kind    string // "settings", "help", "palette", "launcher", "themepicker"; "" when none
	OriginX int
	OriginY int
	Z       int
	Geo     overlay.Geometry
	Rows    []overlayRowHit
}

// overlayKindOrder is the deterministic order newly-opened overlays are added to
// the stack (used only to break ties when several open in the same frame).
//
// "accent" was missing from this list, which cost it a place in the stack and
// left it on the base z-index. Nothing noticed while the colour picker could
// only be opened over the rail, with no other panel to lose a tie against. Now
// that a settings row opens it, the tie was with the panel it was drawn on top
// of, and every click inside the picker went to the settings row behind it.
//
// "effectpicker" and "sectioneditor" were missing the same way, and a settings
// row opens each of them over the panel too. The guard against a fourth is
// TestEveryOverlayKindHasAPlaceInTheStack, which reads openOverlayKinds itself
// rather than a second copy of this list.
var overlayKindOrder = []string{"help", "palette", "launcher", "session", "agentmail", "inbox", "workspace", "layout", "hostpicker", "aggregate", "settings", "keybinds", "themepicker", "glyphpicker", "effectpicker", "dockeditor", "sectioneditor", "accent", "screenshot", "quit", "sessionclose", "filedialog"}

// openOverlayKinds returns the set of draggable overlay kinds currently shown.
func (m *OS) openOverlayKinds() map[string]bool {
	open := map[string]bool{}
	if m.ShotPreview.Open {
		open[overlayKindShot] = true
	}
	if m.ShowHelp {
		open["help"] = true
	}
	if m.ShowCommandPalette {
		open["palette"] = true
	}
	if m.ShowLauncher {
		open["launcher"] = true
	}
	if m.ShowSessionSwitcher {
		open["session"] = true
	}
	if m.ShowAgentMail {
		open["agentmail"] = true
	}
	if m.ShowInbox {
		open["inbox"] = true
	}
	if m.ShowWorkspaceSwitcher {
		open["workspace"] = true
	}
	if m.ShowLayoutPicker {
		open["layout"] = true
	}
	if m.ShowHostPicker {
		open["hostpicker"] = true
	}
	if m.ShowAggregateView {
		open["aggregate"] = true
	}
	if m.ShowSettings {
		open["settings"] = true
	}
	if m.ShowKeybindManager {
		open["keybinds"] = true
	}
	if m.ShowThemePicker {
		open["themepicker"] = true
	}
	if m.ShowGlyphPicker {
		open["glyphpicker"] = true
	}
	if m.ShowEffectPicker {
		open["effectpicker"] = true
	}
	if m.ShowDockEditor {
		open["dockeditor"] = true
	}
	if m.ShowSectionEditor {
		open["sectioneditor"] = true
	}
	if m.ShowAccentPicker {
		open["accent"] = true
	}
	if m.ShowQuitMenu {
		open["quit"] = true
	}
	if m.ShowSessionClose {
		open["sessionclose"] = true
	}
	if m.FilePromptOpen() {
		open["filedialog"] = true
	}
	return open
}

// AnyOverlayOpen reports whether any overlay is logically open, independent of
// whether its hit geometry has been recorded yet this frame. The hover and
// focus-follows-mouse routing use it so an overlay guards its first frame too.
func (m *OS) AnyOverlayOpen() bool {
	return len(m.openOverlayKinds()) > 0
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

// centerOrigin returns the top-left screen cell that centers a w by h block on
// the whole screen. Every modal surface lands here: a dialog the user is being
// asked to answer belongs where they are already looking, and the screen centre
// is the only spot that means the same thing at every terminal size.
func (m *OS) centerOrigin(w, h int) (int, int) {
	return max((m.GetRenderWidth()-w)/2, 0), max((m.GetRenderHeight()-h)/2, 0)
}

// overlayOrigin returns the top-left screen cell for an overlay panel: centered,
// shifted by that kind's drag offset, and clamped so the panel stays on screen.
func (m *OS) overlayOrigin(kind string, geo overlay.Geometry) (int, int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	// Centred in the rows under a dock at the top, and kept below it while it
	// fits there. See panelRoomHeight.
	top := min(m.GetTopMargin(), max(rh-geo.Height, 0))
	off := m.overlayOffset(kind)
	x := m.panelCenterX(geo.Width, rw) + off[0]
	y := top + m.overlayAnchorY(kind, geo.Height, rh-top) + off[1]
	x = max(min(x, rw-geo.Width), 0)
	y = max(min(y, rh-geo.Height), top)
	return x, y
}

// panelCenterX is the left column that centres a panel w cells wide: in the
// columns the panes have when it fits there, and on the whole screen when it
// does not.
//
// Centred on the whole screen, a panel that fitted beside the rail still ran
// a few cells into it, and cut the rail mid-word: "erminals", "iles" and
// "gents" down the edge of the help panel, the gutter marks gone beside the
// Inbox. Beside the rail it leaves the rail whole, which is where the agents
// the Inbox is about are listed. The modal dialogs keep the screen centre, see
// centerOrigin: they are small, and ask for an answer where the eye already
// is.
//
// A panel too wide for the panes' columns covers the rail, and covers all of
// it. Centred on the screen it covered all but a column or two, and the rail's
// remnant down the panel's edge read as litter: "e…", "1…", a lone "+" from
// the headers, cut off beside the Inbox on an 80 column screen.
func (m *OS) panelCenterX(w, screenW int) int {
	if room := m.GetContentWidth(); w <= room {
		return m.GetLeftMargin() + (room-w)/2
	}
	return m.railCoverX((screenW-w)/2, w, screenW)
}

// railCoverX moves a block w cells wide that starts at x and runs partly over
// the rail so that it covers the rail completely, since a rail cut down to a
// column or two is only fragments of its rows.
func (m *OS) railCoverX(x, w, screenW int) int {
	if right := m.GetRightMargin(); right > 0 && x+w > screenW-right && x+w < screenW {
		x = screenW - w
	}
	if left := m.GetLeftMargin(); left > 0 && x > 0 && x < left {
		x = 0
	}
	return max(x, 0)
}

// overlayAnchor is the top row a panel was centred at when it opened, and the
// screen height that was measured against.
type overlayAnchor struct {
	y, screenH int
}

// overlayAnchorY is the top row for a panel of height h: centred when it
// opens, and kept there for as long as it stays open.
//
// A panel's height follows what it shows. The Inbox grows a line when a risky
// approval is armed and shrinks when a snooze picker takes its detail, and
// centred afresh every frame it jumped up and down the screen under the
// cursor at each of those, a row or three at a time. Held at its first top it
// grows and shrinks at the bottom edge instead, which is where a reader is not
// looking. A new screen height centres it again.
//
// The hold covers a change of a few rows, not a different panel. The Inbox
// opens on its short empty state and fills when its first item lands; held at
// the empty state's top, the full list sat on the bottom edge with half the
// screen empty above it. A panel whose centred top has moved more than
// overlayAnchorSlack rows from where it is held is centred again and held
// there.
func (m *OS) overlayAnchorY(kind string, h, screenH int) int {
	y := (screenH - h) / 2
	if a, ok := m.overlayAnchors[kind]; ok && a.screenH == screenH &&
		a.y+h <= screenH && abs(a.y-y) <= overlayAnchorSlack {
		return a.y
	}
	if m.overlayAnchors == nil {
		m.overlayAnchors = make(map[string]overlayAnchor)
	}
	m.overlayAnchors[kind] = overlayAnchor{y: y, screenH: screenH}
	return y
}

// overlayAnchorSlack is how far, in rows, a held panel's top may sit from the
// top that would centre it before it is centred again: a risky approval's
// armed line or a snooze picker's footer moves the centre a row or two, and a
// panel that filled with a list moves it by half the list.
const overlayAnchorSlack = 3

// forgetClosedOverlayAnchors drops the anchor of every panel not drawn this
// frame, so a panel opened again is centred again.
func (m *OS) forgetClosedOverlayAnchors() {
	for kind := range m.overlayAnchors {
		if !slices.ContainsFunc(m.OverlayHits, func(h overlayPanelHit) bool { return h.Kind == kind }) {
			delete(m.overlayAnchors, kind)
		}
	}
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
	x, y := m.centerOrigin(lipgloss.Width(box), lipgloss.Height(box))
	return lipgloss.NewLayer(box).X(x).Y(y).Z(z).ID(id)
}

// syncOverlayASCII mirrors the ASCII-only setting into the overlay package,
// which has no dependency on tuios config.
func syncOverlayASCII(s *config.Settings) {
	overlay.SetASCII(s.UseASCIIOnly)
}
