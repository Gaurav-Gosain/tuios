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
func (m *OS) KeyModes() []string { return m.keyModes }

// KeyModeOn reports whether a key mode is on.
func (m *OS) KeyModeOn() bool { return len(m.keyModes) > 0 }

// EnterKeyMode turns on the declared mode name, on top of the modes that
// are on. A mode that is on already, a mode the config does not declare, and
// a mode past config.MaxKeyModes change nothing.
func (m *OS) EnterKeyMode(name string) {
	if _, ok := m.UserConfig.Keybindings.Mode(name); !ok ||
		slices.Contains(m.keyModes, name) || len(m.keyModes) >= config.MaxKeyModes {
		return
	}
	m.keyModes = append(m.keyModes, name)
	m.MarkAllDirty()
}

// LeaveKeyMode turns off the mode at place i of KeyModes, or the top one
// when i is out of range: a once mode that ran a key while a mode above it
// passed the key down is not the top one.
func (m *OS) LeaveKeyMode(i int) {
	if len(m.keyModes) == 0 {
		return
	}
	if i < 0 || i >= len(m.keyModes) {
		i = len(m.keyModes) - 1
	}
	m.keyModes = slices.Delete(m.keyModes, i, i+1)
	m.MarkAllDirty()
}

// PruneKeyModes drops the modes a reloaded config no longer declares.
func (m *OS) PruneKeyModes() {
	n := len(m.keyModes)
	m.keyModes = slices.DeleteFunc(m.keyModes, func(name string) bool {
		_, ok := m.UserConfig.Keybindings.Mode(name)
		return !ok
	})
	if len(m.keyModes) != n {
		m.MarkAllDirty()
	}
}

// keyModeLabel is the chip's text: the titles of the modes that are on, the
// one on top last, so the chip reads in the order they were turned on.
func (m *OS) keyModeLabel() string {
	var b strings.Builder
	for i, name := range m.keyModes {
		if i > 0 {
			b.WriteString(" > ")
		}
		b.WriteString(m.UserConfig.Keybindings.ModeTitle(name))
	}
	return b.String()
}

// keyModeChip is the dock chip drawn while a key mode is on, with a column
// in front of it to keep it off the mode pill, or "" while none is. It names
// the modes and says that esc turns off the top one, in the accent: a mode
// changes what the keys do, and the chip is where a person looks to see why.
func (m *OS) keyModeChip() string {
	if len(m.keyModes) == 0 {
		return ""
	}
	pal := m.groundUI()
	chip := lipgloss.NewStyle().Background(pal.AccentTint).
		Foreground(overlay.Readable(pal.Accent, pal.AccentTint)).Bold(true).
		Render(" " + m.keyModeLabel() + " ")
	hint := []overlay.Hint{{Key: "esc", Label: "leave", Priority: overlay.HintEssential}}
	strip, _ := overlay.FitHintStrip(hint, overlay.HintStripWidth(hint), pal.Panel, pal)
	pad := overlay.Style(pal.Panel).Render(" ")
	return " " + chip + pad + strip + pad
}
