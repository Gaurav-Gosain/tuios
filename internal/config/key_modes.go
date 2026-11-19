package config

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Key modes: named key sets that stay on until they are left, as Rex and
// zellij have them. A mode is a [keybindings.modes.NAME] table:
//
//	[keybindings.modes.resize]
//	title = "Resize"
//	exclusive = true
//
//	[keybindings.modes.resize.keys]
//	resize_pane_left_5 = ["h"]
//	resize_pane_right_5 = ["l"]
//
//	[keybindings.prefix_mode]
//	"enter_mode:resize" = ["R"]
//
// leader R turns the mode on, and h and l then resize until esc. Any section
// binds enter_mode:NAME, a mode or a menu too, so modes stack: the mode on
// top reads a key first, and a mode that is not exclusive passes a key it
// does not bind to the mode under it, and then to the pane or the window
// keys. An exclusive mode drops the keys it does not bind. once turns the
// mode off after one key it reads. esc, when no mode on the stack binds it,
// turns off the mode on top, and leave_mode does the same from a key.
//
// A menu closes after one key and a sticky menu after a key it does not
// bind. A mode stays on through any key, so the pane can be typed into while
// a mode that is not exclusive is on. Nothing here ships: a mode exists
// only when a config declares it.

// EnterModePrefix starts the action that turns on a declared key mode.
const EnterModePrefix = "enter_mode:"

// ModeSectionPrefix starts the section name of a declared mode's keys, as
// the report, doctor and unbind name the table: "modes.resize".
const ModeSectionPrefix = "modes."

// ActionLeaveMode turns off the key mode on top of the stack.
const ActionLeaveMode = "leave_mode"

// MaxKeyModes is how many key modes can be on at once. A mode that turns on
// another one past this does nothing.
const MaxKeyModes = 8

// ModeConfig is one key mode: a [keybindings.modes.NAME] table.
type ModeConfig struct {
	// Title is what the dock's mode chip says. The mode's name when empty.
	Title string `toml:"title,omitempty"`
	// Exclusive drops the keys the mode does not bind, so no key reaches
	// the pane or the modes under it while the mode is on.
	Exclusive bool `toml:"exclusive,omitempty"`
	// Once turns the mode off after one key it reads: a key it binds, or,
	// in an exclusive mode, any key.
	Once bool `toml:"once,omitempty"`
	// Keys binds actions to keys, as every other section does.
	Keys map[string][]string `toml:"keys,omitempty"`
}

// EnterModeAction is the action that turns on the mode called name.
func EnterModeAction(name string) string { return EnterModePrefix + name }

// ModeNameOf is the mode an enter_mode:NAME action turns on.
func ModeNameOf(action string) (string, bool) {
	name, ok := strings.CutPrefix(action, EnterModePrefix)
	return name, ok && name != ""
}

// ModeNames is the modes tuios uses, in name order: the ones with a valid
// name. validateModes warns about the others.
func (k *KeybindingsConfig) ModeNames() []string {
	if len(k.Modes) == 0 {
		return nil
	}
	names := make([]string, 0, len(k.Modes))
	for name := range k.Modes {
		if validMenuName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Mode returns the mode called name.
func (k *KeybindingsConfig) Mode(name string) (ModeConfig, bool) {
	if !validMenuName(name) {
		return ModeConfig{}, false
	}
	m, ok := k.Modes[name]
	return m, ok
}

// ModeTitle is what the dock's chip calls the mode.
func (k *KeybindingsConfig) ModeTitle(name string) string {
	if m, ok := k.Mode(name); ok && strings.TrimSpace(m.Title) != "" {
		return strings.TrimSpace(m.Title)
	}
	return name
}

// modeSection is the keys of the mode a "modes.NAME" section name refers to.
func (k *KeybindingsConfig) modeSection(section string) (map[string][]string, bool) {
	name, ok := strings.CutPrefix(section, ModeSectionPrefix)
	if !ok {
		return nil, false
	}
	m, ok := k.Mode(name)
	return m.Keys, ok
}

// modeScopes is the scope of each declared mode, for ScopesFor. Its chord is
// the first press found that turns the mode on, from the scopes and the
// menus, or enter_mode:NAME when nothing turns it on. A mode that is not
// exclusive takes its keys from the pane while it is on; an exclusive one
// owns the keyboard.
func modeScopes(kb *KeybindingsConfig, scopes []Scope) []Scope {
	names := kb.ModeNames()
	if len(names) == 0 {
		return nil
	}
	chordOf := func(name string) string {
		action := EnterModeAction(name)
		for _, s := range scopes {
			for _, sec := range s.Sections {
				for _, key := range kb.section(sec)[action] {
					if key = strings.TrimSpace(key); key != "" {
						return press(s.Chord, key)
					}
				}
			}
		}
		return action
	}
	out := make([]Scope, 0, len(names))
	for _, name := range names {
		reach := ReachSteals
		if kb.Modes[name].Exclusive {
			reach = ReachModal
		}
		out = append(out, Scope{
			ID: "mode." + name, Name: kb.ModeTitle(name) + " mode", Chord: chordOf(name),
			Sections: []string{ModeSectionPrefix + name},
			Reaches:  reach,
		})
	}
	return out
}

// validateModes warns about a mode tuios cannot use, a key that turns on a
// mode that is not there, and a mode nothing turns on.
func validateModes(cfg *UserConfig, result *ValidationResult) {
	kb := &cfg.Keybindings
	for _, name := range slices.Sorted(maps.Keys(kb.Modes)) {
		if !validMenuName(name) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "modes", Key: name,
				Message: fmt.Sprintf("The mode name %q is not usable and the mode is ignored. Use lowercase letters, digits, - and _.", name),
			})
		}
	}
	entered := map[string]bool{}
	for _, section := range keySections(kb) {
		for _, action := range sortedActions(section.keys) {
			target, ok := ModeNameOf(action)
			if !ok || len(section.keys[action]) == 0 {
				continue
			}
			if _, declared := kb.Mode(target); !declared {
				result.Warnings = append(result.Warnings, ValidationError{
					Field: section.name, Key: action,
					Message: fmt.Sprintf("%s turns on a mode that is not declared, so the key does nothing. Add a [keybindings.modes.%s] table with keys.", action, target),
				})
				continue
			}
			entered[target] = true
		}
	}
	for _, name := range kb.ModeNames() {
		if !entered[name] && !menuRowEnters(cfg, name) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "modes", Key: name,
				Message: fmt.Sprintf("Nothing turns on the mode %s. Bind %s in a section, for example [keybindings.prefix_mode].", name, EnterModeAction(name)),
			})
		}
	}
}

// menuRowEnters reports whether a [menus.NAME] row turns on the mode, as
// a row of a right-click menu can.
func menuRowEnters(cfg *UserConfig, name string) bool {
	action := EnterModeAction(name)
	for _, spec := range cfg.Menus {
		for _, list := range [][]any{spec.Items, spec.Add} {
			rows, _ := menuEntries(list)
			for _, e := range rows {
				if e.Action == action {
					return true
				}
			}
		}
	}
	return false
}
