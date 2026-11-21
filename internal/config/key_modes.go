package config

import (
	"sort"
	"strings"
)

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

func (k *KeybindingsConfig) modeSection(section string) (map[string][]string, bool) {
	name, ok := strings.CutPrefix(section, ModeSectionPrefix)
	if !ok {
		return nil, false
	}
	m, ok := k.Mode(name)
	return m.Keys, ok
}

func modeScopes(kb *KeybindingsConfig) []Scope {
	names := kb.ModeNames()
	out := make([]Scope, 0, len(names))
	for _, name := range names {
		reach := ReachSteals
		if kb.Modes[name].Exclusive {
			reach = ReachModal
		}
		out = append(out, Scope{
			ID: "mode." + name, Name: kb.ModeTitle(name) + " mode", Chord: EnterModeAction(name),
			Sections: []string{ModeSectionPrefix + name},
			Reaches:  reach,
		})
	}
	return out
}
