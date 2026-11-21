package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

var charmToneHex = map[string]string{
	"Cumin":    "#BF976F",
	"Coral":    "#FF577D",
	"Sriracha": "#EB4268",
	"Tuna":     "#FF6DAA",
	"Macaron":  "#E940B0",
	"Dolly":    "#FF60FF",
	"Blush":    "#FF84FF",
	"Urchin":   "#C337E0",
	"Charple":  "#6B50FF",
	"Hazy":     "#8B75FF",
	"Jelly":    "#4A30D9",
	"Sapphire": "#4949FF",
	"Guppy":    "#7272FF",
	"Oceania":  "#2B55B3",
	"Damson":   "#007AB8",
	"Malibu":   "#00A4FF",
	"Sardine":  "#4FBEFE",
	"Guac":     "#12C78F",
	"Julep":    "#00FFB2",
	"Mustard":  "#F5EF34",
	"Zest":     "#E8FE96",
	"Pepper":   "#201F26",
	"BBQ":      "#2D2C36",
	"Char":     "#3A3943",
	"Oyster":   "#605F6B",
	"Squid":    "#858392",
	"Steam":    "#A2A0AD",
	"Smoke":    "#BFBCC8",
	"Steep":    "#D6D3DC",
	"Sash":     "#ECEBF0",
	"Salt":     "#F7F6FB",
	"Pickle":   "#00A475",
	"Gator":    "#18463D",
	"Pom":      "#AB2454",
}

type charmToneTheme struct {
	id, name                 string
	dark                     bool
	fg, bg, cursor, selected string
	ansi                     [16]string
	accent, accentBright     string
	success, warning, error  string
	info                     string
}

var charmToneThemes = [...]charmToneTheme{{
	id: "charmtone", name: "CharmTone", dark: true,
	fg: "Sash", bg: "Pepper", cursor: "Dolly", selected: "Charple",
	ansi: [16]string{
		"BBQ", "Coral", "Guac", "Mustard", "Charple", "Dolly", "Malibu", "Smoke",
		"Squid", "Tuna", "Julep", "Zest", "Guppy", "Blush", "Sardine", "Salt",
	},
	accent: "Charple", accentBright: "Hazy",
	success: "Julep", warning: "Mustard", error: "Sriracha", info: "Malibu",
}, {
	id: "charmtone-light", name: "CharmTone Light",
	fg: "Char", bg: "Salt", cursor: "Charple", selected: "Steep",
	ansi: [16]string{
		"Pepper", "Pom", "Pickle", "Cumin", "Charple", "Urchin", "Damson", "Squid",
		"Oyster", "Sriracha", "Gator", "Cumin", "Sapphire", "Macaron", "Oceania", "Steam",
	},
	accent: "Charple", accentBright: "Jelly",
	success: "Pickle", warning: "Cumin", error: "Sriracha", info: "Damson",
}}

func registerCharmToneThemes() {
	for _, th := range charmToneThemes {
		c := func(name string) *tint.Color { return tint.FromHex(charmToneHex[name]) }
		t := &tint.Tint{
			ID: th.id, DisplayName: th.name, Dark: th.dark,
			Fg: c(th.fg), Bg: c(th.bg), Cursor: c(th.cursor), SelectionBg: c(th.selected),
		}
		slots := [16]**tint.Color{
			&t.Black, &t.Red, &t.Green, &t.Yellow, &t.Blue, &t.Purple, &t.Cyan, &t.White,
			&t.BrightBlack, &t.BrightRed, &t.BrightGreen, &t.BrightYellow,
			&t.BrightBlue, &t.BrightPurple, &t.BrightCyan, &t.BrightWhite,
		}
		for i, slot := range slots {
			*slot = c(th.ansi[i])
		}
		tint.Register(t)
		col := func(name string) color.Color { return AsColor(c(name)) }
		registerChrome(th.id, &Chrome{
			Accent:       col(th.accent),
			AccentBright: col(th.accentBright),
			Success:      col(th.success),
			Warning:      col(th.warning),
			Error:        col(th.error),
			Info:         col(th.info),
		})
	}
}
