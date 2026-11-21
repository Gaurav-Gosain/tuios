package app

import (
	"image"
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

type modalOverlay struct {
	id    string
	open  func(*OS) bool
	scrim bool
}

var modalOverlays = [...]modalOverlay{
	{id: "palette", open: func(m *OS) bool { return m.ShowCommandPalette }, scrim: true},
	{id: "launcher", open: func(m *OS) bool { return m.ShowLauncher }, scrim: true},
	{id: "goto", open: func(m *OS) bool { return m.gotoPicker.open }, scrim: true},
	{id: "agentmail", open: func(m *OS) bool { return m.ShowAgentMail }, scrim: true},
	{id: "inbox", open: func(m *OS) bool { return m.ShowInbox }},
	{id: overlayKindBuffers, open: func(m *OS) bool { return m.buffers.open }, scrim: true},
	{id: overlayKindTransfers, open: func(m *OS) bool { return m.transfers.open }, scrim: true},
	{id: "navigator", open: func(m *OS) bool { return m.navigator.open }, scrim: true},
	{id: "layout", open: func(m *OS) bool { return m.ShowLayoutPicker }, scrim: true},
	{id: "hostpicker", open: func(m *OS) bool { return m.ShowHostPicker }, scrim: true},
	{id: "settings", open: func(m *OS) bool { return m.ShowSettings }, scrim: true},
	{id: "keybinds", open: func(m *OS) bool { return m.ShowKeybindManager }, scrim: true},
	{id: "themepicker", open: func(m *OS) bool { return m.ShowThemePicker }},
	{id: "themeeditor", open: func(m *OS) bool { return m.themeEditor.open }},
	{id: "presetpicker", open: func(m *OS) bool { return m.presetPicker.open }, scrim: true},
	{id: "dockeditor", open: func(m *OS) bool { return m.ShowDockEditor }},
	{id: "sectioneditor", open: func(m *OS) bool { return m.ShowSectionEditor }},
	{id: "glyphpicker", open: func(m *OS) bool { return m.ShowGlyphPicker }},
	{id: "effectpicker", open: func(m *OS) bool { return m.ShowEffectPicker }},
	{id: "accent", open: func(m *OS) bool { return m.ShowAccentPicker }},
	{id: "rename", open: func(m *OS) bool { return m.Renaming() }, scrim: true},
	{id: overlayKindShot, open: func(m *OS) bool { return m.ShotPreview.Open }, scrim: true},
	{id: "quit", open: func(m *OS) bool { return m.ShowQuitMenu }, scrim: true},
	{id: "sessionclose", open: func(m *OS) bool { return m.ShowSessionClose }, scrim: true},
	{id: overlayKindRestart, open: func(m *OS) bool { return m.DaemonRestartOpen() }, scrim: true},
	{id: "filedialog", open: func(m *OS) bool { return m.FilePromptOpen() }, scrim: true},
	{id: "worktreeprompt", open: func(m *OS) bool { return m.WorktreePromptOpen() }, scrim: true},
	{id: overlayKindCopyDialog, open: func(m *OS) bool { return m.CopyDialogOpen() }, scrim: true},
	{id: overlayKindPluginApproval, open: func(m *OS) bool { return m.PluginApprovalOpen() }, scrim: true},
	{id: overlayKindIntegrationAsk, open: func(m *OS) bool { return m.IntegrationAskOpen() }, scrim: true},
	{id: overlayKindMachineSheet, open: func(m *OS) bool { return m.MachineSheetOpen() }, scrim: true},
	{id: overlayKindTaskCapture, open: func(m *OS) bool { return m.TaskCaptureOpen() }, scrim: true},
	{id: overlayKindUpgradeConfirm, open: func(m *OS) bool { return m.UpgradeConfirmOpen() }, scrim: true},
	{id: "help", open: func(m *OS) bool { return m.ShowHelp }, scrim: true},
	{id: "tape-manager", open: func(m *OS) bool { return m.ShowTapeManager }, scrim: true},
	{id: "tape-review", open: func(m *OS) bool { return m.ShowTapeReview }, scrim: true},
	{id: overlayKindLogs, open: func(m *OS) bool { return m.ShowLogs }, scrim: true},
	{id: overlayKindMessage, open: func(m *OS) bool { return m.msgView.open }, scrim: true},
	{id: "cache-stats", open: func(m *OS) bool { return m.ShowCacheStats }, scrim: true},
}

const maxModalOverlays = 64

const sidebarLayerID = "sidebar"

func init() {
	if len(modalOverlays) > maxModalOverlays {
		panic("modalOverlays lists more overlays than the open set has bits for")
	}
}

var modalIndex = func() map[string]int {
	idx := make(map[string]int, len(modalOverlays))
	for i, o := range modalOverlays {
		idx[o.id] = i
	}
	return idx
}()

func scrimBehind(id string) bool {
	i, ok := modalIndex[id]
	return ok && modalOverlays[i].scrim
}

func (m *OS) applyScrim(canvas *frameCanvas, dim int) {
	m.applyScrimIn(canvas, canvas.Bounds(), dim)
}

func (m *OS) applyScrimIn(canvas *frameCanvas, area image.Rectangle, dim int) {
	area = area.Intersect(canvas.Bounds())
	if area.Empty() {
		return
	}
	if dim <= 0 {
		return
	}
	faintOnly := theme.Depth() == overlay.Depth16
	s := &m.motion.scrim
	if !faintOnly {
		s.setToward(m.scrimToward())
		s.syncGround()
		s.syncLevels(dim)
		s.run.have = false
	}
	const level = config.SpotlightLevels - 1
	for _, line := range canvas.Lines[area.Min.Y:area.Max.Y] {
		for x := area.Min.X; x < min(area.Max.X, len(line)); x++ {
			cell := &line[x]
			if cell.Content == "" || vt.IsKittyPlaceholder(cell.Content) {
				continue
			}
			wasFaint := cell.Style.Attrs&uv.AttrFaint != 0
			if faintOnly {
				cell.Style.Attrs |= uv.AttrFaint
			} else {
				s.dimCell(cell, level)
			}
			if !wasFaint && faintIsInvisible(cell) {
				cell.Style.Attrs &^= uv.AttrFaint
			}
		}
	}
}

func faintIsInvisible(cell *uv.Cell) bool {
	return cell.Content == " " && isNilColor(cell.Style.Bg) &&
		cell.Style.Attrs&uv.AttrReverse == 0 && cell.Style.Underline == 0
}

func (m *OS) scrimToward() color.Color {
	if t := theme.Current(); t != nil {
		if theme.GroundIsLight(t.Bg) {
			return theme.AsColor(t.Bg)
		}
		return nil
	}
	if m.host.bg != nil && m.host.light {
		return m.host.bg
	}
	return nil
}

func (m *OS) scrimDialogGround(canvas *frameCanvas, r image.Rectangle, dim int) {
	r = r.Intersect(canvas.Bounds())
	if r.Empty() || dim <= 0 || theme.Depth() == overlay.Depth16 {
		return
	}
	corner := canvas.Lines[r.Min.Y]
	if r.Min.X >= len(corner) {
		return
	}
	ground := corner[r.Min.X].Style.Bg
	s := &m.motion.scrim
	const level = config.SpotlightLevels - 1
	for _, line := range canvas.Lines[r.Min.Y:r.Max.Y] {
		for x := r.Min.X; x < min(r.Max.X, len(line)); x++ {
			cell := &line[x]
			if cell.Content == "" || vt.IsKittyPlaceholder(cell.Content) || !sameGround(cell.Style.Bg, ground) {
				continue
			}
			fg, attrs := cell.Style.Fg, cell.Style.Attrs
			s.dimCell(cell, level)
			cell.Style.Fg, cell.Style.Attrs = fg, attrs
		}
	}
}

func sameGround(a, b color.Color) bool {
	if isNilColor(a) || isNilColor(b) {
		return isNilColor(a) && isNilColor(b)
	}
	return sameColor(a, b)
}
