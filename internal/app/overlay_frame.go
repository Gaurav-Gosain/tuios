package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// overlayUI is the palette this client draws its dialogs and panels with.
//
// With appearance.pane_overlays on, an overlay reads as a pane window: its
// frame in the colour the focused pane's border is drawn in, and the frame and
// everything inside it on the ground an empty pane cell shows, with the
// cursor row, the hovered row and the cards stepped from that ground (see
// theme.PaneUI). A panel is framed then, since a panel on the pane's ground
// has no other edge. appearance.overlay_border_color colours the frames
// whether the option is on or not. With neither set this is theme.UI().
//
// What it reads is this client's own: its mode, its session's tint, its copy
// mode, its terminal's background. The SSH and web servers run a model per
// client in one process, so none of it goes through a package variable; the
// palette carries it to the overlay renderers.
func (m *OS) overlayUI() overlay.Palette {
	p := theme.UI()
	if g := m.overlayGround(); g != nil {
		p = theme.PaneUI(g)
		p.Framed = true
	}
	p.Frame = m.overlayFrameInk()
	return p
}

// overlayGround is the ground a pane-styled overlay is filled with: the pane
// background when one is painted, else the terminal's own background, which
// is what an empty pane cell shows. It is nil, and the overlay keeps the
// chrome's surface, when the option is off, at 16 colours where every ground
// is the terminal's own, when the border style draws no frame (a dialog's
// shade is then its only edge), and with no theme when the terminal never
// said what its background is.
func (m *OS) overlayGround() color.Color {
	if !m.Settings.PaneOverlays || theme.Depth() == overlay.Depth16 || !overlay.FrameDrawn() {
		return nil
	}
	if g := m.paneGround(); g.on() {
		return g.bg
	}
	if theme.Current() == nil && m.host.bg == nil {
		// terminalBg guesses black here, and painting a guess over a light
		// terminal would be a black slab.
		return nil
	}
	return solidColor(m.terminalBg())
}

// overlayFrameInk is the colour overlay frames are drawn in:
// appearance.overlay_border_color when it is set, else the focused pane's
// border colour under appearance.pane_overlays, else nil, which keeps the
// palette's own.
func (m *OS) overlayFrameInk() color.Color {
	if c := theme.ConfigColor(m.Settings.OverlayBorderColor); !isNilColor(c) {
		return c
	}
	if !m.Settings.PaneOverlays {
		return nil
	}
	return m.focusedBorderColor(m.focusedStateBorder(m.GetFocusedWindow()))
}
