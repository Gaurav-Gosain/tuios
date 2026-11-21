package theme

import (
	"image/color"

	tint "github.com/lrstanley/bubbletint/v2"
)

type craftedTheme struct {
	id, name                 string
	dark                     bool
	fg, bg, cursor, selected string
	ansi                     [16]string
	accent, accentBright     string
	surface, canvas, panel   string
}

var craftedThemes = [...]craftedTheme{{
	id: "ember", name: "Ember", dark: true,
	fg: "#E8DCCF", bg: "#1A1614", cursor: "#FF9E57", selected: "#3A2A20",
	ansi: [16]string{
		"#2A2320", "#E0675A", "#A8B868", "#E8B04F", "#6FA0D4", "#C58DB0", "#6FC0A6", "#D9CCBE",
		"#7D6E62", "#F0837A", "#BDCB82", "#F5C873", "#8DB8E2", "#D6A6C4", "#8AD2BA", "#F5EDE4",
	},
	accent: "#FF9E57", accentBright: "#FFC08A",
	surface: "#241E1B", canvas: "#110E0C", panel: "#151210",
}, {
	id: "boreal", name: "Boreal", dark: true,
	fg: "#D6E2F0", bg: "#0E1420", cursor: "#7CF2C5", selected: "#1F3550",
	ansi: [16]string{
		"#18202F", "#F07A8A", "#6FE3B0", "#E6D28A", "#6EA8FF", "#B69CFF", "#5ED6E6", "#C8D4E3",
		"#64738C", "#FF9AA8", "#96F0C8", "#F2E2A8", "#93C0FF", "#CDB8FF", "#8AE6F0", "#EEF4FB",
	},
	accent: "#7CF2C5", accentBright: "#B69CFF",
	surface: "#152033", canvas: "#090D16", panel: "#0B111B",
}, {
	id: "plum", name: "Plum", dark: true,
	fg: "#E6DAE6", bg: "#1E1622", cursor: "#E8A0C8", selected: "#3B2840",
	ansi: [16]string{
		"#2B2030", "#E4707E", "#9CC79A", "#E3BF7A", "#8FA6E0", "#D08CD0", "#82C2C6", "#D6C8D8",
		"#7E6A84", "#F08C98", "#B4D8B0", "#F0D29A", "#AABCEC", "#E0A8E0", "#A0D4D6", "#F6EEF6",
	},
	accent: "#D08CD0", accentBright: "#E8A0C8",
	surface: "#2A1F2F", canvas: "#140E17", panel: "#18111B",
}, {
	id: "dune", name: "Dune", dark: false,
	fg: "#3B3229", bg: "#F4ECDD", cursor: "#B5562A", selected: "#E4D3B4",
	ansi: [16]string{
		"#3B3229", "#A8402E", "#5A7224", "#8A6410", "#2F5F8A", "#8A4778", "#2C6E6A", "#7A6E60",
		"#8A7D6C", "#C25236", "#6E8A2E", "#A07818", "#3D74A6", "#A35990", "#36837E", "#A89A86",
	},
	accent: "#A64B22", accentBright: "#8F3F1C",
}, {
	id: "fjord", name: "Fjord", dark: false,
	fg: "#243240", bg: "#EEF3F7", cursor: "#2F64A8", selected: "#D3E2EE",
	ansi: [16]string{
		"#243240", "#B23A4A", "#2E7A4E", "#8A6A00", "#2F64A8", "#7A4FB0", "#1C7484", "#6D7C8A",
		"#7C8B99", "#C8505E", "#3B8F5E", "#9E7C10", "#3D78C2", "#8E62C4", "#2A8C9C", "#9AA8B4",
	},
	accent: "#2F64A8", accentBright: "#1F4F8F",
}}

func registerCraftedThemes() {
	for _, th := range craftedThemes {
		c := tint.FromHex
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
		col := func(hex string) color.Color { return AsColor(c(hex)) }
		ch := &Chrome{
			Accent:       col(th.accent),
			AccentBright: col(th.accentBright),
			Success:      col(th.ansi[2]),
			Warning:      col(th.ansi[3]),
			Error:        col(th.ansi[1]),
			Info:         col(th.ansi[4]),
		}
		if th.surface != "" {
			ch.Surface = col(th.surface)
			ch.Canvas = col(th.canvas)
			ch.Panel = col(th.panel)
			ch.deriveRamp()
		}
		registerChrome(t.ID, ch)
	}
}
