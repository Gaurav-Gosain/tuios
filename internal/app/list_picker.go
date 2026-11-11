package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// A list picker is an overlay that lists rows, filters them as you type, and
// does something with the row the cursor is on. What it holds is one
// listnav.State; what it does is one listPicker, so the keys, the wheel, a
// click, a hover and a paste reach every picker by the same rules:
//
//   - The picker's own keys first (keys), such as ctrl+d to delete a row.
//   - Then esc closes and enter accepts.
//   - Then the list keys of [keybindings.lists] move the cursor, under
//     appearance.wrap_lists. A picker with a filter keeps its letters for
//     the filter, so only the keys that are not letters move it.
//   - Then the filter line takes the key (textedit.Line), and a change to the
//     query refilters: the cursor goes back to the first row that can be
//     chosen.
//   - The wheel moves one row and never wraps; a hover moves the cursor; a
//     click moves it and accepts.
//
// Each picker keeps what is its own: its rows, how a row draws, what enter
// does, a preview, its extra keys.

// listPicker is the behaviour of one picker. Each function takes the model and
// reads the picker's rows through it; state is where the picker's State lives.
type listPicker struct {
	kind string
	// state is the picker's list state on m.
	state func(m *OS) *listnav.State
	// count is how many rows the list holds as filtered now.
	count func(m *OS) int
	// visible is how many rows the picker shows. nil: listOverlayRows.
	visible func(m *OS) int
	// rest reports whether row i can hold the cursor. nil: every row can.
	rest func(m *OS, i int) bool
	// first is the row the cursor goes to after a refilter. nil: row 0.
	first func(m *OS) int
	// keys reads the picker's own keys before any other. It reports whether
	// it took the key.
	keys func(m *OS, key string) (tea.Cmd, bool)
	// accept runs enter, and a click, on the row the cursor is on.
	accept func(m *OS) tea.Cmd
	// moved runs after the cursor moved, for a picker that previews the row.
	moved func(m *OS) tea.Cmd
	// refiltered runs after the query changed the rows. nil: nothing more.
	refiltered func(m *OS) tea.Cmd
	// close puts the picker away without accepting: esc and a click away.
	close func(m *OS)
	// clearKey lets ctrl+u clear the filter.
	clearKey bool
}

func (p *listPicker) rows(m *OS) int { return p.count(m) }

func (p *listPicker) shown(m *OS) int {
	if p.visible != nil {
		return p.visible(m)
	}
	return listOverlayRows
}

func (p *listPicker) restFor(m *OS) func(int) bool {
	if p.rest == nil {
		return nil
	}
	return func(i int) bool { return p.rest(m, i) }
}

func (p *listPicker) afterMove(m *OS, moved bool) tea.Cmd {
	if !moved || p.moved == nil {
		return nil
	}
	return p.moved(m)
}

// refilter puts the cursor back at the top after the query changed.
func (p *listPicker) refilter(m *OS) tea.Cmd {
	first := 0
	if p.first != nil {
		first = p.first(m)
	}
	p.state(m).Refiltered(first)
	if p.refiltered != nil {
		return p.refiltered(m)
	}
	return nil
}

// key reads one key press. key is the key as a command reads it (the key on
// a Latin layout for a key from another layout), and msg is the press as it
// came, for the text it types. It reports whether the picker took the key.
func (p *listPicker) key(m *OS, key string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if p.keys != nil {
		if cmd, ok := p.keys(m, key); ok {
			return cmd, true
		}
	}
	st := p.state(m)
	switch key {
	case "esc":
		p.close(m)
		return nil, true
	case "enter":
		return p.accept(m), true
	}
	if motion := m.ListKeys.Keys(key, false); motion != listnav.None {
		return p.move(m, listnav.Delta(motion, listOverlayRows)), true
	}
	changed, consumed := st.Query.Edit(msg.String(), msg.Text, textedit.LineOpts{AllowClear: p.clearKey})
	if changed {
		return p.refilter(m), true
	}
	return nil, consumed
}

// move moves the cursor delta rows under the list rule and the session's wrap
// setting.
func (p *listPicker) move(m *OS, delta int) tea.Cmd {
	moved := p.state(m).Move(delta, p.rows(m), p.shown(m), m.listWraps(), p.restFor(m))
	return p.afterMove(m, moved)
}

// paste types s into the filter.
func (p *listPicker) paste(m *OS, s string) tea.Cmd {
	if !p.state(m).Query.Insert(s) {
		return nil
	}
	return p.refilter(m)
}

// wheel moves the cursor one row for a turn of the wheel.
func (p *listPicker) wheel(m *OS, up bool) tea.Cmd {
	moved := p.state(m).Wheel(up, p.rows(m), p.shown(m), p.restFor(m))
	return p.afterMove(m, moved)
}

// hover moves the cursor to row i under the pointer.
func (p *listPicker) hover(m *OS, i int) tea.Cmd {
	if p.rest != nil && !p.rest(m, i) {
		return nil
	}
	return p.afterMove(m, p.state(m).Select(i, p.rows(m), p.shown(m)))
}

// click moves the cursor to row i and accepts it.
func (p *listPicker) click(m *OS, i int) tea.Cmd {
	if i < 0 || i >= p.rows(m) || p.rest != nil && !p.rest(m, i) {
		return nil
	}
	p.state(m).Select(i, p.rows(m), p.shown(m))
	return p.accept(m)
}

// searchLine draws the filter line with its caret, after the sigil.
func (p *listPicker) searchLine(m *OS, width int) string {
	pal := m.overlayUI()
	bg := pal.Surface
	sigil := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())
	before, at, after := p.state(m).Query.Parts()
	return sigil + overlay.FieldLine(before, at, after, max(width-lipgloss.Width(overlay.Sigil()), 1), bg, pal.Fg)
}

// openListPicker is the picker the keyboard is on, or nil when no list picker
// is open. The order is the order overlay_keys.go routes keys in.
func (m *OS) openListPicker() *listPicker {
	switch {
	case m.ShowEffectPicker:
		return effectPicker()
	case m.ShowLayoutPicker && m.LayoutPickerMode != "save":
		return layoutPicker()
	case m.ShowHostPicker:
		return hostPicker()
	}
	return nil
}

// listPickerOfKind is the list picker an overlay kind names, or nil.
//
// Each picker is built by a function, not held in a package variable: a
// picker's functions reach, through accept and the settings it saves, the
// code that draws the overlays, which reads the picker back, and Go refuses
// that cycle in a package-level initializer. A function has no initializer,
// and building the small struct per event costs nothing worth keeping.
func listPickerOfKind(kind string) *listPicker {
	switch kind {
	case "effectpicker":
		return effectPicker()
	case "layout":
		return layoutPicker()
	case "hostpicker":
		return hostPicker()
	}
	return nil
}

// ListPickerKey hands a key press to the list picker the keyboard is on. key
// is the key as a command reads it. It reports whether a picker took the key;
// a picker that is open takes every key, so nothing reaches the pane under it.
func (m *OS) ListPickerKey(key string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	p := m.openListPicker()
	if p == nil {
		return nil, false
	}
	cmd, _ := p.key(m, key, msg)
	return cmd, true
}

// ListPickerPaste types a paste into the filter of the list picker the
// keyboard is on. It reports whether a picker took it.
func (m *OS) ListPickerPaste(s string) (tea.Cmd, bool) {
	p := m.openListPicker()
	if p == nil {
		return nil, false
	}
	return p.paste(m, s), true
}
