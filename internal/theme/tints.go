package theme

import tint "github.com/lrstanley/bubbletint/v2"

const builtinTintSlots = 20

type builtinCredit struct{ name, link string }

type builtinTintInfo struct {
	display, id string
	dark        bool
	credits     []builtinCredit
}

func builtinTints() (def *tint.Tint, all []*tint.Tint) {
	all = make([]*tint.Tint, len(builtinTintTable))
	for i, info := range builtinTintTable {
		t := &tint.Tint{DisplayName: info.display, ID: info.id, Dark: info.dark}
		if info.credits != nil {
			t.CreditSources = make([]*tint.CreditSource, 0, len(info.credits))
		}
		for _, c := range info.credits {
			t.CreditSources = append(t.CreditSources, &tint.CreditSource{Name: c.name, Link: c.link})
		}
		slots := [builtinTintSlots]**tint.Color{
			&t.Fg, &t.Bg, &t.SelectionBg, &t.Cursor,
			&t.BrightBlack, &t.BrightBlue, &t.BrightCyan, &t.BrightGreen,
			&t.BrightPurple, &t.BrightRed, &t.BrightWhite, &t.BrightYellow,
			&t.Black, &t.Blue, &t.Cyan, &t.Green,
			&t.Purple, &t.Red, &t.White, &t.Yellow,
		}
		data := builtinTintColors[i*builtinTintSlots*4:]
		for j, slot := range slots {
			c := data[j*4 : j*4+4]
			if c[0] == 0 {
				continue
			}
			*slot = &tint.Color{R: c[1], G: c[2], B: c[3], A: 255}
		}
		all[i] = t
		if t.ID == builtinDefaultTint {
			def = t
		}
	}
	return def, all
}

func newBuiltinRegistry() {
	def, all := builtinTints()
	tint.DefaultRegistry = tint.NewRegistry(def, all...)
}
