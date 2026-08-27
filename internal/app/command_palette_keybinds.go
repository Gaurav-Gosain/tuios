package app

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// PaletteCategoryKeybind tags the palette rows that lead into the keybind
// manager.
const PaletteCategoryKeybind = "Keybind"

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
// words would only make the list harder to read. The row opens the manager
// filtered to that action, where every one of its bindings is listed and can be
// rebound or unbound separately.
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
		action := e.action
		items = append(items, CommandPaletteItem{
			// The action name is in the row so the fuzzy filter finds it: a user
			// who read config.toml searches for "prefix_close_window", not for
			// "Close current window".
			Name:     e.desc + "  " + action,
			Shortcut: shortcut,
			Category: PaletteCategoryKeybind,
			Keybind:  true,
			Action: func(m *OS) (*OS, tea.Cmd) {
				// Filtered by the action name, which is unique and which the
				// manager's filter matches against, so the overlay opens on that
				// action's bindings and nothing else.
				m.OpenKeybindManagerWith(action)
				return m, nil
			},
		})
	}
	return items
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
	n := 0
	for _, item := range items {
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
