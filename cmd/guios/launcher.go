package main

import (
	"encoding/json"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The launcher (parity gap 18): the programs on the PATH of the machine the
// session runs on, as the TUI's launcher lists them (run_anything.go), and
// one chosen runs in a new pane there. The daemon of that machine lists
// them (list-programs), so a session on a linked host lists that host's.
// Only programs: guios has no desktop entry to start, and Open covers files.

// openLauncher lists the programs in the palette, for the shown session.
func (a *App) openLauncher() {
	s := a.session(a.selected)
	if s == nil {
		a.toast(locales.LauncherNoSession())
		return
	}
	sid := s.ID
	a.verbOn(sid, "list-programs", nil, func(raw json.RawMessage, err error) {
		var res struct {
			Programs []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"programs"`
		}
		if err != nil || json.Unmarshal(raw, &res) != nil {
			a.toast(locales.LauncherFailed())
			return
		}
		if len(res.Programs) == 0 {
			a.toast(locales.LauncherEmpty())
			return
		}
		items := make([]paletteItem, 0, len(res.Programs))
		for _, p := range res.Programs {
			name, path := p.Name, p.Path
			items = append(items, paletteItem{title: name, detail: path, words: name + " " + path,
				run: func() { a.runProgram(sid, name, path) }})
		}
		a.openPalette()
		a.pal.custom, a.pal.customLabel = items, locales.LauncherLabel()
	})
}

// runProgram opens a pane in session sid running the program at path. The
// argv goes with the request, so no shell parses it; the pane closes when
// the program exits, as the TUI's launcher does.
func (a *App) runProgram(sid, name, path string) {
	a.verbOn(sid, "new-window", map[string]any{"name": name, "command": []string{path}}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.toast(locales.LauncherRunFailed(name, err.Error()))
		}
	})
}
