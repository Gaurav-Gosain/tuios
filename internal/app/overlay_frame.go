package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// An overlay with a drawn frame reads as a pane window: its frame in the
// colour the focused pane's border is drawn in, and the frame and everything
// inside it on the ground an empty pane cell shows. See overlay.FrameColors.

// syncOverlayFrame pushes what overlay frames are drawn with: the focused
// pane's border colour, which is what the panes show for the focused thing,
// and the pane's ground.
func (m *OS) syncOverlayFrame() {
	ground := m.overlayGround()
	measure := ground
	if measure == nil {
		measure = m.terminalBg()
	}
	overlay.SetFrameColors(overlay.FrameColors{
		Ink:     m.focusedBorderColor(m.focusedStateBorder(m.GetFocusedWindow())),
		Ground:  ground,
		Measure: solidColor(measure),
	})
}

// overlayGround is the ground a framed overlay is filled with: what an empty
// cell of a pane shows. That is the pane background when one is painted.
// When none is, an empty pane cell is left on the terminal's default
// background, and so is the overlay's cell, nil, which comes out the same:
// except that the compositor gives a layer's uncoloured cells the desktop's
// ground, a pane's not among them. With the desktop painted, the overlay is
// filled with the terminal's own background instead, as the terminal said it,
// which is the colour its default background shows.
func (m *OS) overlayGround() color.Color {
	if g := m.paneGround(); g.on() {
		return g.bg
	}
	if !m.surfaceGround(surfaceDesktop).on() || m.host.bg == nil {
		return nil
	}
	bg := solidColor(m.host.bg)
	if theme.Depth() == overlay.Depth256 {
		return overlay.To256(bg)
	}
	return bg
}
