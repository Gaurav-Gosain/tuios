package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// HoldModeAction is the keybinding whose key, while physically held, puts tuios
// in window-management mode and hands the previous mode back on release.
const HoldModeAction = "hold_window_mode"

// holdMode is the momentary window-management mode.
//
// It rests entirely on key events: the trigger's press arms it and the trigger's
// release disarms it. Nothing polls and nothing is scheduled, so an idle tick
// costs exactly what it did before.
type holdMode struct {
	active bool
	// returnMode is where the release goes back to. Only used when the hold's
	// own mode is still the one in effect, so an action taken while held (which
	// is the point of the mode) keeps whatever mode it chose.
	returnMode Mode
	// strip is the modifier the trigger key stands for, taken off every chord
	// struck while it is held. Holding Option and tapping n has to run the
	// window-mode action bound to n, not the one bound to alt+n.
	strip tea.KeyMod
}

// modifierKeyMods maps the modifier keys a terminal can report as keys of their
// own (Kitty protocol, report-all-keys) to the modifier bit they set on every
// other key while held.
var modifierKeyMods = map[string]tea.KeyMod{
	"leftalt": tea.ModAlt, "rightalt": tea.ModAlt,
	"leftctrl": tea.ModCtrl, "rightctrl": tea.ModCtrl,
	"leftsuper": tea.ModSuper, "rightsuper": tea.ModSuper,
	"leftmeta": tea.ModMeta, "rightmeta": tea.ModMeta,
	"lefthyper": tea.ModHyper, "righthyper": tea.ModHyper,
	"leftshift": tea.ModShift, "rightshift": tea.ModShift,
}

// HoldModeKey returns the key configured to hold window mode, lower-cased, or ""
// when the feature is unbound (the default).
func (m *OS) HoldModeKey() string {
	if m.KeybindRegistry == nil {
		return ""
	}
	for _, key := range m.KeybindRegistry.GetKeys(HoldModeAction) {
		if key = strings.ToLower(strings.TrimSpace(key)); key != "" {
			return key
		}
	}
	return ""
}

// HoldModeNeedsAllKeys reports whether the configured trigger is a modifier key.
// A terminal only reports those as keys of their own under the Kitty protocol's
// report-all-keys-as-escape-codes flag, which is why tuios asks for it only when
// the trigger needs it: it turns every keystroke in the session into an escape
// code, and that is too much to impose on someone not using this.
func (m *OS) HoldModeNeedsAllKeys() bool {
	_, isModifier := modifierKeyMods[m.HoldModeKey()]
	return isModifier
}

// HoldModeAvailable reports whether hold-to-mode can work here: a trigger is
// configured and the terminal has not told us it cannot report key releases.
//
// A terminal that never answered the enhancement query is not refused, because
// it cannot hurt: without the protocol it never sends the trigger's press event
// either, so the feature degrades to doing nothing at all rather than to a mode
// nobody can leave.
func (m *OS) HoldModeAvailable() bool {
	if m.HoldModeKey() == "" {
		return false
	}
	return m.KeyboardFlags == 0 || m.KeyboardFlags&ansi.KittyReportEventTypes != 0
}

// HoldModeUnsupportedReason explains why a configured hold key does nothing, or
// "" when it works. Shown once the terminal has answered, so the user is not
// left pressing a key that silently does nothing.
func (m *OS) HoldModeUnsupportedReason() string {
	if m.HoldModeKey() == "" || m.HoldModeAvailable() {
		return ""
	}
	return "Hold-to-window mode needs the Kitty keyboard protocol. " +
		"This terminal does not report key releases, so " + m.HoldModeKey() + " does nothing."
}

// HoldModeActive reports whether the trigger is being held right now. The
// renderer reads this the way it reads Mode, since the user has to be able to
// see which mode they are in.
func (m *OS) HoldModeActive() bool {
	return m.hold.active
}

// PressHoldKey arms the momentary mode. It reports whether the key was the
// trigger and has been consumed.
func (m *OS) PressHoldKey(key tea.KeyPressMsg) bool {
	trigger := m.HoldModeKey()
	if trigger == "" || !strings.EqualFold(key.Keystroke(), trigger) {
		return false
	}
	if !m.HoldModeAvailable() {
		return false
	}
	switch {
	case !m.hold.active:
		m.hold = holdMode{active: true, returnMode: m.Mode, strip: modifierKeyMods[trigger]}
		m.Mode = WindowManagementMode
	case key.IsRepeat:
		// The key is still down; the terminal is just repeating it.
	default:
		// A second press with no release in between means the release was lost.
		// Ending the hold here is the one reading that cannot stay stuck.
		m.EndHold()
	}
	return true
}

// ReleaseHoldKey ends the momentary mode if the released key is the trigger. It
// reports whether it was.
func (m *OS) ReleaseHoldKey(key tea.KeyPressMsg) bool {
	trigger := m.HoldModeKey()
	if trigger == "" || !strings.EqualFold(key.Keystroke(), trigger) {
		return false
	}
	m.EndHold()
	return true
}

// EndHold leaves the momentary mode and restores the mode the hold interrupted.
// Restoring is skipped when something done while held has already chosen a mode
// of its own, which is the point of being able to act while holding.
func (m *OS) EndHold() {
	if !m.hold.active {
		return
	}
	returnMode := m.hold.returnMode
	m.hold = holdMode{}
	if m.Mode == WindowManagementMode {
		m.Mode = returnMode
	}
}

// StripHoldModifier takes the trigger's own modifier off a chord struck while it
// is held, so the chord resolves to the key the user tapped.
func (m *OS) StripHoldModifier(key tea.KeyPressMsg) tea.KeyPressMsg {
	if !m.hold.active || m.hold.strip == 0 {
		return key
	}
	key.Mod &^= m.hold.strip
	return key
}
