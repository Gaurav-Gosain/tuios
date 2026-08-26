package config

import (
	"slices"
	"sort"
	"strings"
)

// Unbinding a key is written to config.toml as an empty list on the action:
//
//	[keybindings.terminal_mode]
//	terminal_focus_left = []
//
// An action the file does not mention at all is filled in from the defaults at
// load (fillMapDefaults keys off whether the action is present, not off how
// many keys it has). An action present with an empty list is left alone, so the
// default does not come back on the next load or the next restart. That is the
// whole encoding: absent means "I have no opinion", present-and-empty means "I
// took this away".
//
// The two things a user means by unbinding are both reached from here:
//
//   - Take one key away from tuios so the program in the pane gets it. That is
//     FreeKey, which strips the key from every action in every scope, because a
//     key half-freed still never reaches the pane.
//   - Leave an action with no key at all. That is UnbindAction, or UnbindKey on
//     the action's last key.
//
// Neither can free the leader key or a key the input path takes without asking
// the config. StillHeldBy says which of those apply, so a caller can report a
// key it did not manage to hand back rather than claiming it did.

// Removal is one key taken off one action.
type Removal struct {
	Section string `json:"section"`
	Action  string `json:"action"`
	Key     string `json:"key"`
	// LeftUnbound is true when this was the action's last key, so the action is
	// now written as an empty list.
	LeftUnbound bool `json:"left_unbound"`
}

// UnbindKey takes one key off one action, leaving the action present with an
// empty list when it was the last one. It reports whether anything changed.
//
// The action is never deleted from the map. Deleting it would put it back to
// "not mentioned", which fillMissingKeybinds reads as a request for the
// default, and the binding would return on the next load.
func (k *KeybindingsConfig) UnbindKey(section, action, key string) (Removal, bool) {
	table := k.section(section)
	if table == nil {
		return Removal{}, false
	}
	keys, ok := table[action]
	if !ok {
		return Removal{}, false
	}
	want := lookupForm(key)
	kept := slices.DeleteFunc(slices.Clone(keys), func(k string) bool {
		return lookupForm(k) == want
	})
	if len(kept) == len(keys) {
		return Removal{}, false
	}
	// A non-nil empty slice, so the TOML writer emits "action = []" rather than
	// leaving the action out of the file.
	if kept == nil {
		kept = []string{}
	}
	table[action] = kept
	return Removal{
		Section:     section,
		Action:      action,
		Key:         key,
		LeftUnbound: len(kept) == 0,
	}, true
}

// UnbindAction takes every key off one action, so the action has no key at all
// and the default does not come back. It reports the keys it removed.
func (k *KeybindingsConfig) UnbindAction(section, action string) ([]Removal, bool) {
	table := k.section(section)
	if table == nil {
		return nil, false
	}
	keys, ok := table[action]
	if !ok || len(keys) == 0 {
		return nil, false
	}
	out := make([]Removal, 0, len(keys))
	for _, key := range keys {
		out = append(out, Removal{Section: section, Action: action, Key: key, LeftUnbound: true})
	}
	table[action] = []string{}
	return out, true
}

// FreeKey takes one key off every action in every section, so nothing in the
// config claims it any more. It reports what it removed, in section order.
//
// Every scope at once is the point. A key bound in window mode and in the
// prefix table is still a key the user has to press twice to be rid of, and a
// key left on one of the two scopes that reach the pane (global and
// terminal_mode) still never arrives there. Freeing one action at a time is
// what UnbindKey is for.
func (k *KeybindingsConfig) FreeKey(key string) []Removal {
	want := lookupForm(key)
	var out []Removal
	for _, name := range SectionNames() {
		table := k.section(name)
		if table == nil {
			continue
		}
		actions := make([]string, 0, len(table))
		for action := range table {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			if !slices.ContainsFunc(table[action], func(k string) bool {
				return lookupForm(k) == want
			}) {
				continue
			}
			if r, ok := k.UnbindKey(name, action, key); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

// StillHeldBy names every reason the key does not reach the pane's program
// after the config has stopped claiming it, or nil when nothing holds it.
//
// Two things survive FreeKey. The leader is keybindings.leader_key rather than
// an entry in a section, so it is moved rather than unbound. The built-in
// terminal-mode keys are literals in the input path with no config entry at
// all. Saying so is the difference between "freed" and "the config no longer
// claims it, and it still will not arrive".
func (r *KeybindRegistry) StillHeldBy(key string) []string {
	var out []string
	for _, s := range r.TerminalModeSwallowed() {
		if lookupForm(s.Key) != lookupForm(key) || s.Origin != "built-in" {
			continue
		}
		if s.Action == "leader" {
			out = append(out, "the leader key. Set keybindings.leader_key to move it")
			continue
		}
		out = append(out, strings.ToLower(s.Desc)+", which is built in and not configurable")
	}
	return out
}

// UnboundActions returns every action a section leaves with no keys, sorted.
// These are the deliberate removals: fillMissingKeybinds never creates one.
func (k *KeybindingsConfig) UnboundActions(section string) []string {
	table := k.section(section)
	if table == nil {
		return nil
	}
	var out []string
	for action, keys := range table {
		if liveKeyCount(keys) == 0 {
			out = append(out, action)
		}
	}
	sort.Strings(out)
	return out
}

// liveKeyCount is how many of a list's entries are real keys. A whitespace-only
// entry is one Bindings already skips, so it must not make an action look bound.
func liveKeyCount(keys []string) int {
	n := 0
	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			n++
		}
	}
	return n
}
