package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Named colours for the few pieces of furniture that are not drawn from the
// chrome palette (UI) because they stand for something outside it: the macOS
// window controls, the link under the pointer, the key-cast pills. They were
// literals in the render code; they live here so every colour a frame can
// hold is defined in the token packages, and each answers for the 16-colour
// depth with a slot the terminal paints from its own palette.

// WindowDots are the close, minimise and zoom controls' colours: the macOS
// traffic lights. The render code measures them against the ground they land
// on and lifts one that misses its floor.
func WindowDots() (closeDot, minimise, zoom color.Color) {
	return slotAt16(9, lipgloss.Color("#ff5f57")),
		slotAt16(11, lipgloss.Color("#febc2e")),
		slotAt16(10, lipgloss.Color("#28c840"))
}

// LinkHover is the ink of the link under the pointer in a pane.
func LinkHover() color.Color {
	return slotAt16(14, lipgloss.Color("#7DCFFF"))
}

// BorderMultifocus is the border of a window that is one of several focused
// at once. It is slot 3, yellow, at every depth: the terminal's own yellow.
func BorderMultifocus() color.Color { return overlay.Slot(3) }

// ScrollIndicator is the ink of the scrollback position on a window's bottom
// border.
func ScrollIndicator() color.Color {
	return slotAt16(11, lipgloss.Color("#fbbf24"))
}

// CursorFallback is the pair a drawn cursor cell uses when the cell under it
// names no colour of its own: white on black, which reverses into a block.
func CursorFallback() (fg, bg color.Color) {
	return slotAt16(15, lipgloss.Color("#FFFFFF")), slotAt16(0, lipgloss.Color("#000000"))
}

// ShowkeysKey is the key-cast pill for an ordinary key: its ground and ink.
func ShowkeysKey() (bg, fg color.Color) {
	return slotAt16(8, lipgloss.Color("#3a3a5e")), slotAt16(15, lipgloss.Color("#ffffff"))
}

// ShowkeysLeader is the key-cast pill for the leader key: its ground and ink.
func ShowkeysLeader() (bg, fg color.Color) {
	return slotAt16(14, lipgloss.Color("#00d9ff")), slotAt16(0, lipgloss.Color("#000000"))
}

// SpotlightShade is the colour the spotlight carries an unlit cell toward:
// black, the model of a light going out.
func SpotlightShade() color.Color { return spotlightShade }

// spotlightShade is boxed once, because the spotlight reads it for every cell.
var spotlightShade color.Color = color.RGBA{A: 0xFF}
