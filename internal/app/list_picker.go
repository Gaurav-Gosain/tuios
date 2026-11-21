package app

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type listPicker struct {
	kind       string
	state      func(m *OS) *listnav.State
	count      func(m *OS) int
	visible    func(m *OS) int
	page       func(m *OS) int
	rescroll   func(m *OS)
	rest       func(m *OS, i int) bool
	first      func(m *OS) int
	keys       func(m *OS, key string) (tea.Cmd, bool)
	accept     func(m *OS) tea.Cmd
	moved      func(m *OS) tea.Cmd
	refiltered func(m *OS) tea.Cmd
	close      func(m *OS) tea.Cmd
	clearKey   bool
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
	if moved && p.rescroll != nil {
		p.rescroll(m)
	}
	if !moved || p.moved == nil {
		return nil
	}
	return p.moved(m)
}

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

func (p *listPicker) key(m *OS, key string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if p.keys != nil {
		if cmd, ok := p.keys(m, key); ok {
			return cmd, true
		}
	}
	st := p.state(m)
	switch key {
	case "esc":
		return p.close(m), true
	case "enter":
		return p.accept(m), true
	}
	if motion := m.ListKeys.Keys(key, false); motion != listnav.None {
		page := listOverlayRows
		if p.page != nil {
			page = p.page(m)
		}
		return p.move(m, listnav.Delta(motion, page)), true
	}
	changed, consumed := st.Query.Edit(msg.String(), msg.Text, textedit.LineOpts{AllowClear: p.clearKey})
	if changed {
		return p.refilter(m), true
	}
	return nil, consumed
}

func (p *listPicker) move(m *OS, delta int) tea.Cmd {
	moved := p.state(m).Move(delta, p.rows(m), p.shown(m), m.listWraps(), p.restFor(m))
	return p.afterMove(m, moved)
}

func (p *listPicker) paste(m *OS, s string) tea.Cmd {
	if !p.state(m).Query.Insert(s) {
		return nil
	}
	return p.refilter(m)
}

func (p *listPicker) wheel(m *OS, up bool) tea.Cmd {
	moved := p.state(m).Wheel(up, p.rows(m), p.shown(m), p.restFor(m))
	return p.afterMove(m, moved)
}

func (p *listPicker) hover(m *OS, i int) tea.Cmd {
	if p.rest != nil && !p.rest(m, i) {
		return nil
	}
	return p.afterMove(m, p.state(m).Select(i, p.rows(m), p.shown(m)))
}

func (p *listPicker) click(m *OS, i int) tea.Cmd {
	if i < 0 || i >= p.rows(m) || p.rest != nil && !p.rest(m, i) {
		return nil
	}
	p.state(m).Select(i, p.rows(m), p.shown(m))
	return p.accept(m)
}

func (p *listPicker) searchLine(m *OS, width int) string {
	return searchLine(p.state(m), width, m.overlayUI())
}

func searchLine(st *listnav.State, width int, pal overlay.Palette) string {
	bg := pal.Surface
	sigil := overlay.Style(bg).Foreground(theme.Readable(pal.AccentBright, bg)).Bold(true).Render(overlay.Sigil())
	before, at, after := st.Query.Parts()
	return sigil + overlay.FieldLine(before, at, after, max(width-lipgloss.Width(overlay.Sigil()), 1), bg, pal.Fg)
}

func (m *OS) openListPicker() *listPicker {
	switch {
	case m.prompt.req != nil:
		return palettePicker()
	case m.gotoPicker.open:
		return gotoPicker()
	case m.ShowCommandPalette:
		return palettePicker()
	case m.ShowLauncher:
		return launcherPicker()
	case m.ShowThemePicker:
		return themePicker()
	case m.ShowGlyphPicker:
		return glyphPicker()
	case m.ShowEffectPicker:
		return effectPicker()
	case m.ShowLayoutPicker && m.LayoutPickerMode != "save":
		return layoutPicker()
	case m.ShowHostPicker:
		return hostPicker()
	}
	return nil
}

func listPickerOfKind(kind string) *listPicker {
	switch kind {
	case "goto":
		return gotoPicker()
	case "palette":
		return palettePicker()
	case "launcher":
		return launcherPicker()
	case "themepicker":
		return themePicker()
	case "glyphpicker":
		return glyphPicker()
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

// ListPickerShows is the filter and the cursor row of the list picker the
// keyboard is on, or "" and -1 when none is open.
func (m *OS) ListPickerShows() (query string, cursor int) {
	p := m.openListPicker()
	if p == nil {
		return "", -1
	}
	st := p.state(m)
	return st.Query.String(), st.Cursor
}
