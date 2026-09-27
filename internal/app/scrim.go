package app

import (
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The scrim is the screen behind a modal overlay turned down, so the panel
// reads as the thing in front and the busy pane under it stops competing for
// the eye. Every reference app does it (Textual's palette, opencode, Posting,
// Elia), and it is the cheapest depth cue a terminal has.
//
// Where it runs. composeLayers draws the layers in z order, and the scrim is
// applied to the canvas once, immediately before the first modal overlay's
// layer is drawn. Everything under that layer is darkened (panes, borders, the
// rail, the dock) and nothing the modal draws is, because it has not been drawn
// yet: the contrast floors the chrome is held to inside the panel are the ones
// the panel was designed against. A layer above the modal (a notification, the
// context menu, a tooltip) is drawn after the scrim and stays lit.
//
// Why the canvas and not the panes. Dimming each pane's content where it is
// rendered, as dim_unfocused does, would put the modal in every pane's cache
// key, so every pane would re-render when a modal opens and again when it
// closes, and the borders, the rail and the dock would still need a pass of
// their own. On the canvas no pane is re-rendered at all: the cached layers
// are copied in as always and the pass edits the composed cells in place. It
// costs one walk over the canvas on the frames drawn while a modal is open,
// and a modal left open over a quiet screen draws no frames.
//
// How it darkens. It is the spotlight's shade (see cellShade): every colour it
// can read channels off is carried toward black by the configured percent,
// through the same 16-level cache, so a frame of a few dozen colours costs a
// few dozen blends and no allocations. At 16 colours no blend can be drawn, so
// the text goes faint instead and the grounds, which are the terminal's own,
// stay as they are.

// modalOverlay is one overlay the screen is dimmed behind and that fades in.
type modalOverlay struct {
	// id is the layer id the overlay is drawn under.
	id string
	// open reports whether the overlay is up.
	open func(*OS) bool
	// scrim is whether the screen is dimmed behind it. It is false for the
	// pickers whose preview is the screen behind them: the theme, accent and
	// glyph pickers restyle the chrome as the selection moves, the effect
	// picker plays its effect there, and the dock and section editors change
	// the dock and the rail. Dimming what is being previewed would make the
	// preview wrong.
	scrim bool
}

// modalOverlays lists every modal overlay. It is the one table the scrim, the
// fade and the open tracking read, so an overlay added here gets all three.
// Transient chrome is not here on purpose: the which-key popup, the dock, the
// context menu, tooltips, notifications and the key cast are not modal and the
// screen behind them is still the thing being worked on.
var modalOverlays = [...]modalOverlay{
	{id: "palette", open: func(m *OS) bool { return m.ShowCommandPalette }, scrim: true},
	{id: "launcher", open: func(m *OS) bool { return m.ShowLauncher }, scrim: true},
	{id: "session", open: func(m *OS) bool { return m.ShowSessionSwitcher }, scrim: true},
	{id: "agentmail", open: func(m *OS) bool { return m.ShowAgentMail }, scrim: true},
	{id: "inbox", open: func(m *OS) bool { return m.ShowInbox }, scrim: true},
	{id: "workspace", open: func(m *OS) bool { return m.ShowWorkspaceSwitcher }, scrim: true},
	{id: "layout", open: func(m *OS) bool { return m.ShowLayoutPicker }, scrim: true},
	{id: "hostpicker", open: func(m *OS) bool { return m.ShowHostPicker }, scrim: true},
	{id: "settings", open: func(m *OS) bool { return m.ShowSettings }, scrim: true},
	{id: "keybinds", open: func(m *OS) bool { return m.ShowKeybindManager }, scrim: true},
	{id: "themepicker", open: func(m *OS) bool { return m.ShowThemePicker }},
	{id: "dockeditor", open: func(m *OS) bool { return m.ShowDockEditor }},
	{id: "sectioneditor", open: func(m *OS) bool { return m.ShowSectionEditor }},
	{id: "glyphpicker", open: func(m *OS) bool { return m.ShowGlyphPicker }},
	{id: "effectpicker", open: func(m *OS) bool { return m.ShowEffectPicker }},
	{id: "accent", open: func(m *OS) bool { return m.ShowAccentPicker }},
	{id: "rename", open: func(m *OS) bool { return m.Renaming() }, scrim: true},
	{id: "aggregate", open: func(m *OS) bool { return m.ShowAggregateView }, scrim: true},
	{id: overlayKindShot, open: func(m *OS) bool { return m.ShotPreview.Open }, scrim: true},
	{id: "quit", open: func(m *OS) bool { return m.ShowQuitMenu }, scrim: true},
	{id: "sessionclose", open: func(m *OS) bool { return m.ShowSessionClose }, scrim: true},
	{id: "filedialog", open: func(m *OS) bool { return m.FilePromptOpen() }, scrim: true},
	{id: "help", open: func(m *OS) bool { return m.ShowHelp }, scrim: true},
	{id: "tape-manager", open: func(m *OS) bool { return m.ShowTapeManager }, scrim: true},
	{id: "tape-review", open: func(m *OS) bool { return m.ShowTapeReview }, scrim: true},
	{id: "logs", open: func(m *OS) bool { return m.ShowLogs }, scrim: true},
	{id: "cache-stats", open: func(m *OS) bool { return m.ShowCacheStats }, scrim: true},
}

// maxModalOverlays is how many overlays modalOverlays may list: the open set
// is one bit per overlay in a uint64.
const maxModalOverlays = 64

// sidebarLayerID is the rail's layer id, which the shimmer pass follows.
const sidebarLayerID = "sidebar"

func init() {
	if len(modalOverlays) > maxModalOverlays {
		panic("modalOverlays lists more overlays than the open set has bits for")
	}
}

// modalIndex finds an overlay in modalOverlays by its layer id.
var modalIndex = func() map[string]int {
	idx := make(map[string]int, len(modalOverlays))
	for i, o := range modalOverlays {
		idx[o.id] = i
	}
	return idx
}()

// scrimBehind reports whether the layer with this id is a modal the screen is
// dimmed behind. The layer being on the frame is what says the overlay is
// open, so no separate flag can disagree with what is drawn.
func scrimBehind(id string) bool {
	i, ok := modalIndex[id]
	return ok && modalOverlays[i].scrim
}

// applyScrim darkens every cell on the canvas by the configured modal dim. It
// runs once per frame at most, before the first modal layer is drawn.
func (m *OS) applyScrim(canvas *frameCanvas) {
	dim := m.Settings.ModalDim
	if dim <= 0 {
		return
	}
	faintOnly := theme.Depth() == overlay.Depth16
	s := &m.motion.scrim
	if !faintOnly {
		s.syncGround()
		s.syncLevels(dim)
		s.run.have = false
	}
	const level = config.SpotlightLevels - 1
	for _, line := range canvas.Lines {
		for x := range line {
			cell := &line[x]
			// A zero cell is the tail of a wide glyph; styling it would draw
			// it as a cell of its own. A kitty placeholder's foreground is the
			// image's id, not a colour, and a blended one names an image the
			// host has never seen.
			if cell.Content == "" || vt.IsKittyPlaceholder(cell.Content) {
				continue
			}
			wasFaint := cell.Style.Attrs&uv.AttrFaint != 0
			if faintOnly {
				cell.Style.Attrs |= uv.AttrFaint
			} else {
				s.dimCell(cell, level)
			}
			// Faint on a blank that paints no ground draws nothing, but it
			// still breaks the row's style run and costs an SGR pair on the
			// wire each time the run resumes. Leave such a blank as it was.
			if !wasFaint && faintIsInvisible(cell) {
				cell.Style.Attrs &^= uv.AttrFaint
			}
		}
	}
}

// faintIsInvisible reports whether SGR 2 on this cell changes nothing on
// screen: a space with no ground of its own, not reversed and not underlined,
// shows no ink for faint to reach.
func faintIsInvisible(cell *uv.Cell) bool {
	return cell.Content == " " && isNilColor(cell.Style.Bg) &&
		cell.Style.Attrs&uv.AttrReverse == 0 && cell.Style.Underline == 0
}
