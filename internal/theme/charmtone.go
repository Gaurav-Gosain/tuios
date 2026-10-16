package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

// The CharmTone themes: Charm's colour system as a terminal palette.
//
// The colours are CharmTone's, copied from charm.land/tone v0.1.0 (commit
// b528027a84fe), Copyright 2026 Charmbracelet, Inc., Apache License 2.0. They
// are baked in as hex rather than imported so the theme adds no dependency;
// charm.land/tone, which the chrome already uses, holds the same values at the
// version go.mod pins.
//
// The dark theme is the ANSI mapping Crush ships for its default theme,
// charmtone-panther (charmtoneOpts in internal/ui/styles/themes.go of
// github.com/charmbracelet/crush), with one change: bright black is Squid
// rather than Iron. Iron measures 1.93:1 on Pepper, and bright black is the
// colour shells draw autosuggestions and many programs draw comments in.
// Squid is the step Crush itself uses for its quiet text. The cursor is Dolly
// and the selection Charple, Crush's cursor and text selection colours.
//
// Crush ships no light CharmTone theme. The light one keeps the same hue for
// each slot and takes the shade of it that reads on Salt, CharmTone's light
// ground. Two hues have no such shade: yellow is Cumin at 2.48:1 and green is
// Pickle at 2.97:1, the darkest yellow and the darkest true green CharmTone has.
//
// Neither theme names a chrome ramp. The constant ramp tuios draws its dialogs
// on is CharmTone's own neutrals (see UI), with a hand-set grey ramp at 256
// colours that a named ramp would replace with BBQ's nearest entry, navy. On
// the light theme the ramp is built from Salt, as for any light theme.

// charmToneHex are the CharmTone colours the themes use.
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

// charmToneTheme is one CharmTone theme by colour name. ansi is in slot
// order, 0 to 15.
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

// registerCharmToneThemes registers the CharmTone themes and their chrome.
// Runs with the registry, before the custom themes, so a file with the same
// id replaces one.
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
