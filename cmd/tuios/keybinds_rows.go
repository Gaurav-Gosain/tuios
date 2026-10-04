package main

import (
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// keybindRow is one row of `tuios keybinds list`: one action in one scope,
// with every key that runs it there. --json prints these rows as they are.
type keybindRow struct {
	// Scope is the keyboard context the keys act in: a config scope
	// ("window", "prefix.layout", "sidebar.files"), a fixed group
	// ("copy_mode", "mouse"), or "unbound" for an action with no key in any
	// scope.
	Scope string `json:"scope"`
	// ScopeName is the scope as the list titles it.
	ScopeName string `json:"scope_name"`
	// Chord is what is pressed to reach the scope, such as "ctrl+b L". It is
	// already part of every key in Keys.
	Chord string `json:"chord,omitempty"`
	// Section is the config.toml table the keys come from: a section of
	// [keybindings], "command" for a [[keybindings.command]] entry,
	// "copy_pipe" for a [[keybindings.copy_pipe]] entry, or "" for a key
	// that no table binds.
	Section string `json:"section"`
	// Action is the action name, or "" for a fixed key with no action.
	Action string `json:"action"`
	// Keys are the presses that run the action, chord included.
	Keys []string `json:"keys"`
	// Description says what the keys do.
	Description string `json:"description"`
	// Fixed marks a key that tuios reads itself and no config table binds.
	Fixed bool `json:"fixed,omitempty"`
	// Unbound marks an action that has no key. Keys is empty.
	Unbound bool `json:"unbound,omitempty"`
	// Shadowed are keys the config gives this action that another action in
	// the scope takes first. They do not run this action.
	Shadowed []string `json:"shadowed,omitempty"`
}

// keybindScopeUnbound is the scope of an action that no table binds.
const keybindScopeUnbound = "unbound"

// keybindRows builds every row of `tuios keybinds list` for cfg: each
// registry binding by scope, the command and copy-pipe entries, the fixed
// keys the help overlay lists (copy mode, hints mode, the message view, the
// list keys, the mouse), and every described action that has no key.
//
// It is the one source for the list's text and its --json, so the two
// cannot disagree.
func keybindRows(cfg *config.UserConfig) []keybindRow {
	registry := config.NewKeybindRegistry(cfg)
	leader := cfg.Keybindings.LeaderKey
	if leader == "" {
		leader = config.DefaultLeaderKey
	}
	scopes := map[string]config.Scope{}
	for _, s := range config.Scopes(leader) {
		scopes[s.ID] = s
	}

	var rows []keybindRow
	index := map[string]int{}
	seen := map[string]bool{}
	for _, b := range registry.Bindings() {
		seen[b.Action] = true
		id := b.Scope + "\x00" + b.Section + "\x00" + b.Action
		i, ok := index[id]
		if !ok {
			desc := config.ScopedDescription(b.Scope, b.Action)
			if b.Section == config.SectionCommand {
				desc = b.Desc
			}
			i = len(rows)
			index[id] = i
			rows = append(rows, keybindRow{
				Scope:       b.Scope,
				ScopeName:   scopes[b.Scope].Name,
				Chord:       scopes[b.Scope].Chord,
				Section:     b.Section,
				Action:      b.Action,
				Keys:        []string{},
				Description: desc,
			})
		}
		switch {
		case b.Unbound:
			rows[i].Unbound = true
		case b.Shadowed:
			rows[i].Shadowed = append(rows[i].Shadowed, b.Press)
		default:
			rows[i].Keys = append(rows[i].Keys, b.Press)
		}
	}
	// An action whose every key another action took runs from no key.
	for i := range rows {
		if len(rows[i].Keys) == 0 && len(rows[i].Shadowed) > 0 {
			rows[i].Unbound = true
		}
	}

	for _, g := range app.FixedKeyGroups(leader) {
		for _, h := range g.Bindings {
			keys := make([]string, 0, len(h.Keys))
			for _, k := range h.Keys {
				// The help overlay spells a chord "ctrl+b, [". The list spells
				// every chord as presses with spaces, as the registry does.
				keys = append(keys, strings.Replace(k, leader+", ", leader+" ", 1))
			}
			// A fixed row names an action only when it is one a config can
			// bind. spotlight_off is the overlay's name for a row, not an
			// action.
			action := h.Action
			if config.ActionDescriptions[action] == "" {
				action = ""
			}
			rows = append(rows, keybindRow{
				Scope:       g.ID,
				ScopeName:   g.Name,
				Action:      action,
				Keys:        keys,
				Description: h.Description,
				Fixed:       true,
			})
		}
		if g.ID != "copy_mode" {
			continue
		}
		for _, c := range cfg.Keybindings.CopyPipes() {
			rows = append(rows, keybindRow{
				Scope:       g.ID,
				ScopeName:   g.Name,
				Section:     "copy_pipe",
				Keys:        []string{strings.TrimSpace(c.Key)},
				Description: "Yank through a command: " + c.Label(),
			})
		}
	}

	var unbound []string
	for action := range config.ActionDescriptions {
		if !seen[action] {
			unbound = append(unbound, action)
		}
	}
	sort.Strings(unbound)
	for _, action := range unbound {
		rows = append(rows, keybindRow{
			Scope:       keybindScopeUnbound,
			ScopeName:   "No key",
			Action:      action,
			Keys:        []string{},
			Description: config.ActionDescriptions[action],
			Unbound:     true,
		})
	}
	return rows
}
