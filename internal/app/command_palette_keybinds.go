package app

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// PaletteCategoryKeybind tags the palette rows that run an action or lead
// into the keybind manager.
const PaletteCategoryKeybind = "Keybind"

// PaletteRebindKey opens the keybind manager on the action under the cursor,
// which is what enter did on these rows before it ran the action. ctrl+ and a
// letter, like go-to's ctrl+r, because a printable key is part
// of the query and tab reads as completion.
const PaletteRebindKey = "ctrl+e"

// PaletteKeybindToken is the leading character that turns the palette into a
// search over actions rather than over commands: "#close" lists every action
// with "close" in its name, key or description.
//
// A token rather than plain rows in the merged list. There are a few hundred
// actions and around twenty commands, and putting the first group in the
// palette's default view would mean the palette no longer answers the question
// it was opened for. It is spelled the way the "@state" token already is, and
// the three static keybind rows carry it in their meta slot so it is findable
// by typing the words a person would actually type.
const PaletteKeybindToken = "#"

func getKeybindPaletteItems(m *OS) []CommandPaletteItem {
	if m.KeybindRegistry == nil {
		return nil
	}
	type entry struct {
		action  string
		desc    string
		keys    []string
		unbound bool
		runs    bool
	}
	byAction := map[string]*entry{}
	var order []string
	for _, b := range m.KeybindRegistry.Bindings() {
		e := byAction[b.Action]
		if e == nil {
			e = &entry{action: b.Action, desc: b.Desc, unbound: true}
			byAction[b.Action] = e
			order = append(order, b.Action)
		}
		e.runs = e.runs || paletteRunsScope(b.Scope)
		if b.Unbound {
			continue
		}
		e.unbound = false
		if !containsFold(e.keys, b.Press) {
			e.keys = append(e.keys, b.Press)
		}
	}
	sort.Strings(order)

	items := make([]CommandPaletteItem, 0, len(order))
	for _, name := range order {
		e := byAction[name]
		shortcut := strings.Join(e.keys, ", ")
		if e.unbound {
			shortcut = "unbound"
		}
		action, runs := e.action, e.runs
		items = append(items, CommandPaletteItem{
			Name:     e.desc + "  " + action,
			Shortcut: shortcut,
			Bind:     action,
			Category: PaletteCategoryKeybind,
			Keybind:  true,
			Action: func(m *OS) (*OS, tea.Cmd) {
				if runs {
					if cmd, handled := m.runPaletteAction(action); handled {
						return m, cmd
					}
				}
				m.ShowNotification(action+" works only in its own view.", "info", m.Settings.NotificationDuration)
				m.OpenKeybindManagerWith(action)
				return m, nil
			},
		})
	}
	return items
}

func paletteRunsScope(scope string) bool {
	switch scope {
	case config.ScopeGlobal, config.ScopeWindowMode, config.ScopeTerminalMode, config.ScopePrefix:
		return true
	}
	return strings.HasPrefix(scope, config.ScopePrefix+".")
}

func (m *OS) runPaletteAction(name string) (cmd tea.Cmd, handled bool) {
	r := actionRunner.Load()
	if r == nil {
		return nil, false
	}
	handled, cmd = (*r)(name, m)
	m.MarkAllDirty()
	return cmd, handled
}

// RebindPaletteSelection opens the keybind manager filtered to the action on
// the selected row, and does nothing on a row that is not an action. The name
// is unique and the manager's filter matches against it, so the overlay opens
// on that action's bindings and nothing else.
func (m *OS) RebindPaletteSelection() {
	filtered := m.filteredPaletteItems()
	if m.paletteList.Cursor < 0 || m.paletteList.Cursor >= len(filtered) || !filtered[m.paletteList.Cursor].Keybind {
		return
	}
	action := filtered[m.paletteList.Cursor].Bind
	m.CloseCommandPalette()
	m.OpenKeybindManagerWith(action)
}

func containsFold(keys []string, s string) bool {
	for _, k := range keys {
		if strings.EqualFold(k, s) {
			return true
		}
	}
	return false
}

func splitPaletteKeybinds(query string) (keybinds bool, rest string) {
	q := strings.TrimLeft(query, " ")
	if !strings.HasPrefix(q, PaletteKeybindToken) {
		return false, query
	}
	return true, strings.TrimSpace(strings.TrimPrefix(q, PaletteKeybindToken))
}

func paletteReachable(items []CommandPaletteItem, query string) int {
	keybinds, _ := splitPaletteKeybinds(query)
	settings := !keybinds && strings.TrimSpace(query) != ""
	n := 0
	for _, item := range items {
		if item.Setting && !settings {
			continue
		}
		if item.Keybind == keybinds {
			n++
		}
	}
	return n
}

func keybindPaletteHint(rest string) string {
	if rest == "" {
		return "No action to rebind"
	}
	return "No action matches " + rest + ". Type # on its own to list them all."
}
