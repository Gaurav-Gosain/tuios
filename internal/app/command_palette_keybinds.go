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
// letter, like the session switcher's ctrl+r, because a printable key is part
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

// getKeybindPaletteItems is one row per action, whatever it is bound to.
//
// Per action rather than per binding: an action on three keys in two scopes is
// still one thing the user wants to change, and three rows saying the same
// words would only make the list harder to read.
//
// Enter runs the action, after the palette has closed, so it acts on the pane
// and the mode underneath the way its key would. PaletteRebindKey opens the
// manager filtered to that action instead, where every one of its bindings is
// listed and can be rebound or unbound separately. An action that only a view
// of its own reads (the rail, the Inbox, mail, copy mode, the lists) has
// nothing to act on from here, and some of them act on a cursor the person
// cannot see, such as the rail's file delete, so enter on its row opens the
// manager as the rebind key does.
//
// The registry is read directly rather than through buildKeybindReport: the
// palette has no use for the pane facts, and building them would put a /proc
// read on the palette's open path for nothing.
func getKeybindPaletteItems(m *OS) []CommandPaletteItem {
	if m.KeybindRegistry == nil {
		return nil
	}
	type entry struct {
		action string
		desc   string
		keys   []string
		// unbound is true only when no scope binds the action at all. An action
		// unbound in one scope and bound in another is bound.
		unbound bool
		// runs is true when a scope the palette runs from binds the action.
		runs bool
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
		// The meta slot is what the action answers to today, so the list is also
		// the answer to "what is this on at the moment".
		shortcut := strings.Join(e.keys, ", ")
		if e.unbound {
			shortcut = "unbound"
		}
		action, runs := e.action, e.runs
		items = append(items, CommandPaletteItem{
			// The action name is in the row so the fuzzy filter finds it: a user
			// who read config.toml searches for "prefix_close_window", not for
			// "Close current window".
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

// paletteRunsScope reports whether an action bound in scope can run from the
// palette: the scopes window and terminal mode read, the leader and its
// sub-prefixes, and the global keys.
func paletteRunsScope(scope string) bool {
	switch scope {
	case config.ScopeGlobal, config.ScopeWindowMode, config.ScopeTerminalMode, config.ScopePrefix:
		return true
	}
	return strings.HasPrefix(scope, config.ScopePrefix+".")
}

// runPaletteAction runs an action by name through the same dispatcher its key
// goes through. handled is false when nothing here knows the action.
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
	if m.CommandPaletteSelected < 0 || m.CommandPaletteSelected >= len(filtered) || !filtered[m.CommandPaletteSelected].Keybind {
		return
	}
	action := filtered[m.CommandPaletteSelected].Bind
	m.CloseCommandPalette()
	m.OpenKeybindManagerWith(action)
}

// containsFold reports whether keys already holds s, ignoring case.
func containsFold(keys []string, s string) bool {
	for _, k := range keys {
		if strings.EqualFold(k, s) {
			return true
		}
	}
	return false
}

// splitPaletteKeybinds pulls a leading "#" off a query and reports whether the
// palette should be listing actions, and the text left to match on.
//
// A bare "#" lists every action, which is the halfway house the user is in
// while typing the next character, and is also the answer to "what can I
// rebind".
func splitPaletteKeybinds(query string) (keybinds bool, rest string) {
	q := strings.TrimLeft(query, " ")
	if !strings.HasPrefix(q, PaletteKeybindToken) {
		return false, query
	}
	return true, strings.TrimSpace(strings.TrimPrefix(q, PaletteKeybindToken))
}

// paletteReachable is how many rows a query could match at all, which is the
// count the palette measures its result against.
func paletteReachable(items []CommandPaletteItem, query string) int {
	keybinds, _ := splitPaletteKeybinds(query)
	// The setting rows are only offered to a typed query.
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

// keybindPaletteHint is the line the palette shows in place of "no matches"
// when a "#" search found nothing, since an empty list under a token the user
// has just learned reads as the token not working.
func keybindPaletteHint(rest string) string {
	if rest == "" {
		return "No action to rebind"
	}
	return "No action matches " + rest + ". Type # on its own to list them all."
}
