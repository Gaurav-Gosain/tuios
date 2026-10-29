package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

// The Gilt theme: cool obsidian, warm parchment and sovereign gold.
//
// The gold is the chrome accent as well as the yellow. A bubbletint tint has no
// accent, which is why the theme is registered here, as the CharmTone themes
// are, and not added to that library.
//
// Contrast on the obsidian ground: the foreground measures 12.61:1, the gold
// 9.13:1, and the other normal colours between 4.55:1 (red) and 6.98:1 (cyan).
// Bright black is the muted parchment #716957 at 3.53:1. That is under the
// 4.5:1 of body text, and enough for the comments and suggestions a shell
// draws in it.
//
// Black is the raised stone #181B20, one step above the ground, so a cell
// painted black still reads as a panel instead of vanishing into the ground.
//
// The chrome names the whole neutral ramp (surface, canvas and panel), so the
// dialogs sit on the obsidian steps and not on a ramp derived from one surface.

// registerGiltTheme registers the gilt theme and its chrome. Runs with the
// registry, before the custom themes, so a file with the same id replaces it.
func registerGiltTheme() {
	c := tint.FromHex
	t := &tint.Tint{
		ID: "gilt", DisplayName: "Gilt", Dark: true,
		Fg: c("#D8D1C0"), Bg: c("#0D0F12"), Cursor: c("#F5EBD2"), SelectionBg: c("#342D1B"),
		Black: c("#181B20"), Red: c("#BC6060"), Green: c("#7FA37A"), Yellow: c("#D4AF37"),
		Blue: c("#6F8FAF"), Purple: c("#9A7AA0"), Cyan: c("#72A5A4"), White: c("#D8D1C0"),
		BrightBlack: c("#716957"), BrightRed: c("#D47474"), BrightGreen: c("#9DBB8E"),
		BrightYellow: c("#E7C968"), BrightBlue: c("#8FAECC"), BrightPurple: c("#B596B8"),
		BrightCyan: c("#91C0BD"), BrightWhite: c("#F5EBD2"),
	}
	tint.Register(t)
	col := func(hex string) color.Color { return AsColor(c(hex)) }
	ch := &Chrome{
		Accent:       col("#D4AF37"),
		AccentBright: col("#F4E6A6"),
		Success:      col("#7FA37A"),
		Warning:      col("#D4AF37"),
		Error:        col("#BC6060"),
		Info:         col("#6F8FAF"),
		Surface:      col("#181B20"),
		Canvas:       col("#050607"),
		Panel:        col("#090A0C"),
	}
	// The ink tiers are measured on the ramp. A theme file and Omarchy derive
	// them when they are read. Without this the palette's inks are nil.
	ch.deriveRamp()
	registerChrome(t.ID, ch)
}
