package theme

import tint "github.com/lrstanley/bubbletint/v2"

// The built-in themes, rebuilt from the compact table in tints_gen.go rather
// than taken from bubbletint's DefaultTints, whose 342 pointer-heavy literals
// cost about 200 KB of binary. gen_tints.go writes the table and says how.
//
// Ways this can differ from bubbletint's registry, and what covers each:
//   - A table older than the bubbletint in go.mod. tints_test.go compares
//     every tint field for field with DefaultTints.
//   - A colour slot read back into the wrong field. The same test.
//   - An unset slot (SelectionBg and Cursor in most themes) coming back as
//     black. Unset slots are stored as a zero marker and left nil.
//   - Registration order and the starting tint. NewRegistry is called with
//     the same default and the same list, in the same order.

// builtinTintSlots is how many colours each tint has.
const builtinTintSlots = 20

type builtinCredit struct{ name, link string }

type builtinTintInfo struct {
	display, id string
	dark        bool
	credits     []builtinCredit
}

// builtinTints builds every built-in tint, in bubbletint's order, and returns
// the one its default registry starts on.
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

// newBuiltinRegistry is tint.NewDefaultRegistry built from the table.
func newBuiltinRegistry() {
	def, all := builtinTints()
	tint.DefaultRegistry = tint.NewRegistry(def, all...)
}
