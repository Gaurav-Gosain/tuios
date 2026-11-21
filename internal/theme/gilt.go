package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

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
	ch.deriveRamp()
	registerChrome(t.ID, ch)
}
