package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) overlayUI() overlay.Palette {
	p := theme.UI()
	if g := m.overlayGround(); g != nil {
		p = theme.PaneUI(g)
		p.Framed = true
	}
	p.Frame = m.overlayFrameInk()
	return p
}

func (m *OS) overlayGround() color.Color {
	if !m.Settings.PaneOverlays || theme.Depth() == overlay.Depth16 || !overlay.FrameDrawn() {
		return nil
	}
	if g := m.paneGround(); g.on() {
		return g.bg
	}
	if theme.Current() == nil && m.host.bg == nil {
		return nil
	}
	return solidColor(m.terminalBg())
}

func (m *OS) overlayFrameInk() color.Color {
	if c := theme.ConfigColor(m.Settings.OverlayBorderColor); !isNilColor(c) {
		return c
	}
	if !m.Settings.PaneOverlays {
		return nil
	}
	return m.focusedBorderColor(m.focusedStateBorder(m.GetFocusedWindow()))
}
