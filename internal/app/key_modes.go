package app

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Key modes: the [keybindings.modes.NAME] tables, as a stack this client
// holds. See config/key_modes.go for what a mode is and input/key_modes.go
// for how its keys are read.
//
// The stack is the client's, not the session's, as nest mode is: another
// client on the same session keeps its keys, and a client that attaches
// again starts with no mode on. A mode the config no longer declares, after
// a reload, is off.

// KeyModes is the names of the key modes that are on, the first one
// turned on first. The slice is the client's own: do not change it.
func (m *OS) KeyModes() []string {
	return m.keyModes
}

// KeyModeOn reports whether a key mode is on.
func (m *OS) KeyModeOn() bool { return len(m.KeyModes()) > 0 }

// EnterKeyMode turns on the declared mode name, on top of the modes that
// are on. A mode that is on already moves to the top. It reports false, and
// changes nothing, for a mode the config does not declare, and when
// config.MaxKeyModes are on.
func (m *OS) EnterKeyMode(name string) bool {
	if _, ok := m.declaredKeyMode(name); !ok {
		return false
	}
	if i := slices.Index(m.keyModes, name); i >= 0 {
		m.keyModes = slices.Delete(m.keyModes, i, i+1)
	} else if len(m.keyModes) >= config.MaxKeyModes {
		return false
	}
	m.keyModes = append(m.keyModes, name)
	m.MarkAllDirty()
	return true
}

// LeaveKeyMode turns off the mode on top. It reports false when no mode is
// on.
func (m *OS) LeaveKeyMode() bool {
	if len(m.keyModes) == 0 {
		return false
	}
	return m.LeaveKeyModeAt(len(m.keyModes) - 1)
}

// LeaveKeyModeAt turns off the mode at place i of KeyModes, for a once mode
// that ran a key while a mode above it passed the key down.
func (m *OS) LeaveKeyModeAt(i int) bool {
	if i < 0 || i >= len(m.keyModes) {
		return false
	}
	m.keyModes = slices.Delete(m.keyModes, i, i+1)
	m.MarkAllDirty()
	return true
}

// LeaveAllKeyModes turns every mode off.
func (m *OS) LeaveAllKeyModes() {
	if len(m.keyModes) == 0 {
		return
	}
	m.keyModes = m.keyModes[:0]
	m.MarkAllDirty()
}

// declaredKeyMode is the mode called name in the running config.
func (m *OS) declaredKeyMode(name string) (config.ModeConfig, bool) {
	if m.UserConfig == nil {
		return config.ModeConfig{}, false
	}
	return m.UserConfig.Keybindings.Mode(name)
}

// PruneKeyModes drops the modes a reloaded config no longer declares. Every
// path that reloads the keybinding registry calls it.
func (m *OS) PruneKeyModes() {
	if len(m.keyModes) == 0 {
		return
	}
	kept := m.keyModes[:0]
	for _, name := range m.keyModes {
		if _, ok := m.declaredKeyMode(name); ok {
			kept = append(kept, name)
		}
	}
	if len(kept) != len(m.keyModes) {
		m.MarkAllDirty()
	}
	m.keyModes = kept
}

// keyModeLabel is the chip's text: the titles of the modes that are on, the
// one on top last, so the chip reads in the order they were turned on.
func (m *OS) keyModeLabel() string {
	modes := m.KeyModes()
	if len(modes) == 0 || m.UserConfig == nil {
		return ""
	}
	kb := &m.UserConfig.Keybindings
	titles := make([]string, len(modes))
	for i, name := range modes {
		titles[i] = kb.ModeTitle(name)
	}
	return strings.Join(titles, " > ")
}

// keyModeLeaveKey is the key that turns off the mode on top: esc, unless a
// mode binds it, and then the key the top mode binds to leave_mode. Empty
// when there is none.
func (m *OS) keyModeLeaveKey() string {
	modes := m.KeyModes()
	if len(modes) == 0 || m.UserConfig == nil {
		return ""
	}
	top, _ := m.declaredKeyMode(modes[len(modes)-1])
	if keys := top.Keys[config.ActionLeaveMode]; len(keys) > 0 {
		return config.KeyLabel(keys[0])
	}
	for _, name := range modes {
		mode, _ := m.declaredKeyMode(name)
		for _, keys := range mode.Keys {
			if slices.Contains(keys, "esc") {
				return ""
			}
		}
	}
	return "esc"
}

// keyModeChip is the dock chip drawn while a key mode is on, with a column
// in front of it to keep it off the mode pill, or "" while none is. It names
// the modes and the key that turns off the top one, in the accent: a mode
// changes what the keys do, and the chip is where a person looks to see why.
func (m *OS) keyModeChip() string {
	label := m.keyModeLabel()
	if label == "" {
		return ""
	}
	pal := m.groundUI()
	ground := pal.AccentTint
	chip := lipgloss.NewStyle().Background(ground).
		Foreground(overlay.Readable(pal.Accent, ground)).Bold(true).
		Render(" " + label + " ")
	pad := overlay.Style(pal.Panel).Render(" ")
	key := m.keyModeLeaveKey()
	if key == "" {
		return " " + chip + pad
	}
	hint := []overlay.Hint{{Key: key, Label: "leave", Priority: overlay.HintEssential}}
	strip, _ := overlay.FitHintStrip(hint, overlay.HintStripWidth(hint), pal.Panel, pal)
	return " " + chip + pad + strip + pad
}
