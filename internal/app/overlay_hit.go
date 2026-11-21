package app

import (
	"slices"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type overlayRowHit struct {
	Rect overlay.Rect
	Idx  int
	Dec  overlay.Rect
	Inc  overlay.Rect
}

type overlayPanelHit struct {
	Kind    string
	OriginX int
	OriginY int
	Z       int
	Geo     overlay.Geometry
	Rows    []overlayRowHit
}

var overlayKindOrder = []string{"help", "palette", "launcher", "goto", "navigator", "agentmail", "inbox", overlayKindBuffers, overlayKindTransfers, "layout", "hostpicker", "settings", "keybinds", "themepicker", "themeeditor", "presetpicker", "glyphpicker", "effectpicker", "dockeditor", "sectioneditor", "accent", "screenshot", "quit", "sessionclose", overlayKindRestart, "filedialog", overlayKindCopyDialog, "worktreeprompt", overlayKindPluginApproval, overlayKindIntegrationAsk, overlayKindLogs, overlayKindMessage, overlayKindMachineSheet, overlayKindTaskCapture, overlayKindUpgradeConfirm}

type overlayGate struct {
	kind string
	open bool
}

func (m *OS) eachOverlayGate(yield func(kind string, open bool) bool) {
	gates := [...]overlayGate{
		{overlayKindShot, m.ShotPreview.Open},
		{"help", m.ShowHelp},
		{"palette", m.ShowCommandPalette},
		{"launcher", m.ShowLauncher},
		{"goto", m.gotoPicker.open},
		{"agentmail", m.ShowAgentMail},
		{"inbox", m.ShowInbox},
		{overlayKindBuffers, m.buffers.open},
		{overlayKindTransfers, m.transfers.open},
		{"navigator", m.navigator.open},
		{"layout", m.ShowLayoutPicker},
		{"hostpicker", m.ShowHostPicker},
		{"settings", m.ShowSettings},
		{"keybinds", m.ShowKeybindManager},
		{"themepicker", m.ShowThemePicker},
		{"themeeditor", m.themeEditor.open},
		{"presetpicker", m.presetPicker.open},
		{"glyphpicker", m.ShowGlyphPicker},
		{"effectpicker", m.ShowEffectPicker},
		{"dockeditor", m.ShowDockEditor},
		{"sectioneditor", m.ShowSectionEditor},
		{"accent", m.ShowAccentPicker},
		{"quit", m.ShowQuitMenu},
		{"sessionclose", m.ShowSessionClose},
		{overlayKindRestart, m.DaemonRestartOpen()},
		{"filedialog", m.FilePromptOpen()},
		{overlayKindCopyDialog, m.CopyDialogOpen()},
		{"worktreeprompt", m.WorktreePromptOpen()},
		{overlayKindPluginApproval, m.PluginApprovalOpen()},
		{overlayKindIntegrationAsk, m.IntegrationAskOpen()},
		{overlayKindMachineSheet, m.MachineSheetOpen()},
		{overlayKindTaskCapture, m.TaskCaptureOpen()},
		{overlayKindUpgradeConfirm, m.UpgradeConfirmOpen()},
		{overlayKindLogs, m.ShowLogs},
		{overlayKindMessage, m.msgView.open},
	}
	for _, g := range gates {
		if !yield(g.kind, g.open) {
			return
		}
	}
}

func (m *OS) openOverlayKinds() map[string]bool {
	open := map[string]bool{}
	m.eachOverlayGate(func(kind string, on bool) bool {
		if on {
			open[kind] = true
		}
		return true
	})
	return open
}

// AnyOverlayOpen reports whether any overlay is logically open, independent of
// whether its hit geometry has been recorded yet this frame. The hover and
// focus-follows-mouse routing use it so an overlay guards its first frame too,
// and the fullscreen fast path uses it to stand down for any panel.
func (m *OS) AnyOverlayOpen() bool {
	found := false
	m.eachOverlayGate(func(_ string, on bool) bool {
		found = on
		return !on
	})
	return found
}

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

// OverlayStack is the open overlays, bottom first, in the order the mouse
// and the frame stack them. It brings the order up to date first, so an
// overlay opened since the last frame is on top. Keys go to the top one that
// takes keys.
func (m *OS) OverlayStack() []string {
	m.reconcileOverlayZOrder()
	return m.OverlayZOrder
}

func (m *OS) overlayZ(kind string) int {
	if kind == overlayKindShot {
		return config.ZIndexReview + 1
	}
	for i, k := range m.OverlayZOrder {
		if k == kind {
			return config.ZIndexOverlayBase + i
		}
	}
	return config.ZIndexOverlayBase
}

func (m *OS) raiseOverlay(kind string) {
	idx := -1
	for i, k := range m.OverlayZOrder {
		if k == kind {
			idx = i
			break
		}
	}
	if idx < 0 || idx == len(m.OverlayZOrder)-1 {
		return
	}
	m.OverlayZOrder = append(m.OverlayZOrder[:idx], m.OverlayZOrder[idx+1:]...)
	m.OverlayZOrder = append(m.OverlayZOrder, kind)
}

type overlayDragState struct {
	Active  bool
	Kind    string
	OffsetX int
	OffsetY int
}

func (m *OS) overlayOffset(kind string) [2]int {
	if m.OverlayOffsets == nil {
		return [2]int{}
	}
	return m.OverlayOffsets[kind]
}

func (m *OS) setOverlayOffset(kind string, x, y int) {
	if m.OverlayOffsets == nil {
		m.OverlayOffsets = make(map[string][2]int)
	}
	m.OverlayOffsets[kind] = [2]int{x, y}
}

func (m *OS) centerOrigin(w, h int) (int, int) {
	top, room := m.modalBand(h)
	y := max(min(top+(room-h)/2, m.GetRenderHeight()-h), 0)
	return max((m.GetRenderWidth()-w)/2, 0), y
}

func (m *OS) modalBand(h int) (top, room int) {
	rh := m.GetRenderHeight()
	r := m.viewReserve()
	top, room = 0, rh
	if mid := (rh - h) / 2; (r.Top > 0 && mid < r.Top+1) || (r.Bottom > 0 && mid+h > rh-r.Bottom-1) {
		top = min(r.Top, max(rh-h, 0))
		room = rh - top
		if h <= rh-r.Top-r.Bottom {
			room -= r.Bottom
		}
	}
	return top, room
}

func (m *OS) overlayOrigin(kind string, geo overlay.Geometry) (int, int) {
	if !previewsChrome(kind) {
		return m.screenOrigin(kind, geo)
	}
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	r := m.viewReserve()
	top := min(r.Top, max(rh-geo.Height, 0))
	room := rh - top
	if geo.Height <= rh-r.Top-r.Bottom {
		room -= r.Bottom
	}
	off := m.overlayOffset(kind)
	x := m.panelCenterX(geo.Width, rw) + off[0]
	y := top + m.overlayAnchorY(kind, geo.Height, room) + off[1]
	x = max(min(x, rw-geo.Width), 0)
	y = max(min(y, rh-geo.Height), top)
	return x, y
}

func (m *OS) screenOrigin(kind string, geo overlay.Geometry) (int, int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	off := m.overlayOffset(kind)
	x := (rw-geo.Width)/2 + off[0]
	top, room := m.modalBand(geo.Height)
	y := top + m.overlayAnchorY(kind, geo.Height, room) + off[1]
	x = max(min(x, rw-geo.Width), 0)
	y = max(min(y, rh-geo.Height), 0)
	return x, y
}

func previewsChrome(kind string) bool {
	i, ok := modalIndex[kind]
	return ok && !modalOverlays[i].scrim
}

func (m *OS) panelCenterX(w, screenW int) int {
	if room := m.ViewContentWidth(); w <= room {
		return m.viewReserve().Left + (room-w)/2
	}
	return m.railCoverX((screenW-w)/2, w, screenW)
}

func (m *OS) railCoverX(x, w, screenW int) int {
	if right := m.viewReserve().Right; right > 0 && x+w > screenW-right && x+w < screenW {
		x = screenW - w
	}
	if left := m.viewReserve().Left; left > 0 && x > 0 && x < left {
		x = 0
	}
	return max(x, 0)
}

type overlayAnchor struct {
	y, screenH int
}

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

const overlayAnchorSlack = 3

func (m *OS) forgetClosedOverlayAnchors() {
	for kind := range m.overlayAnchors {
		if !slices.ContainsFunc(m.OverlayHits, func(h overlayPanelHit) bool { return h.Kind == kind }) {
			delete(m.overlayAnchors, kind)
		}
	}
}

func (m *OS) placeOverlayPanel(layers []*lipgloss.Layer, kind, content string, geo overlay.Geometry, rows []overlayRowHit) []*lipgloss.Layer {
	x, y := m.overlayOrigin(kind, geo)
	z := m.overlayZ(kind)
	m.OverlayHits = append(m.OverlayHits, overlayPanelHit{Kind: kind, OriginX: x, OriginY: y, Z: z, Geo: geo, Rows: rows})
	return append(layers, lipgloss.NewLayer(content).X(x).Y(y).Z(z).ID(kind))
}

func (m *OS) centeredBoxLayer(box string, z int, id string) *lipgloss.Layer {
	x, y := m.centerOrigin(lipgloss.Width(box), lipgloss.Height(box))
	return lipgloss.NewLayer(box).X(x).Y(y).Z(z).ID(id)
}

func syncOverlaySettings(s *config.Settings) {
	overlay.SetASCII(s.UseASCIIOnly)
	overlay.SetNerdFonts(!s.NerdFontsOff())
	b := s.OverlayBorder()
	overlay.SetFrame(overlay.Frame{
		TopLeft: b.TopLeft, TopRight: b.TopRight,
		BottomLeft: b.BottomLeft, BottomRight: b.BottomRight,
		Horizontal: b.Top, Vertical: b.Left,
	})
	overlay.SetFramePanels(s.FramePanels)
}
