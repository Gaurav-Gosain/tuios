package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// dispatchAction runs the handler for action, if there is one. The third result
// reports whether anything ran, so callers can fall back (forward the key to the
// PTY, or ignore it) when the key is not bound.
func dispatchAction(action string, msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if action == "" {
		return o, nil, false
	}
	dispatcher := GetDispatcher()
	if !dispatcher.HasAction(action) {
		return o, nil, false
	}
	m, cmd := dispatcher.Dispatch(action, msg, o)
	return m, cmd, true
}

// sectionLookup is one of the registry's per-section lookups, named as a method
// expression at the call site. Each prefix reads its own section, so the same
// key can mean different things under different prefixes.
type sectionLookup func(*config.KeybindRegistry, string) string

// runPrefix dispatches the key through one prefix section. An unbound key is
// simply dropped, which is what dismissing a prefix chord should do.
func runPrefix(msg tea.KeyPressMsg, o *app.OS, lookup sectionLookup) (*app.OS, tea.Cmd) {
	if o.KeybindRegistry == nil {
		return o, nil
	}
	m, cmd, _ := dispatchAction(lookup(o.KeybindRegistry, msg.String()), msg, o)
	return m, cmd
}

// HandlePrefixCommand handles the key pressed after the leader key, in either
// mode. An unbound key falls through to the terminal in terminal mode (so the
// leader key does not swallow shell input) and is ignored in window-management
// mode.
func HandlePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.PrefixActive = false

	if o.KeybindRegistry != nil {
		action := o.KeybindRegistry.GetPrefixAction(msg.String())
		if m, cmd, ok := dispatchAction(action, msg, o); ok {
			return m, cmd
		}
	}

	// ctrl+c cancels a prefix everywhere. It is deliberately not configurable:
	// it is the way out when a chord was started by accident.
	if msg.String() == "ctrl+c" {
		return o, nil
	}

	if o.Mode == app.TerminalMode {
		forwardKeyToFocusedWindow(msg, o)
	}
	return o, nil
}

// HandleWorkspacePrefixCommand handles the key after leader+w.
func HandleWorkspacePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.WorkspacePrefixActive = false
	o.PrefixActive = false
	return runPrefix(msg, o, (*config.KeybindRegistry).GetWorkspacePrefixAction)
}

// HandleMinimizePrefixCommand handles the key after leader+m.
func HandleMinimizePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.MinimizePrefixActive = false
	o.PrefixActive = false
	return runPrefix(msg, o, (*config.KeybindRegistry).GetMinimizePrefixAction)
}

// HandleTilingPrefixCommand handles the key after leader+t (the window prefix).
func HandleTilingPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.TilingPrefixActive = false
	o.PrefixActive = false
	return runPrefix(msg, o, (*config.KeybindRegistry).GetWindowPrefixAction)
}

// HandleDebugPrefixCommand handles the key after leader+D.
func HandleDebugPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.DebugPrefixActive = false
	o.PrefixActive = false
	return runPrefix(msg, o, (*config.KeybindRegistry).GetDebugPrefixAction)
}

// HandleTapePrefixCommand handles the key after leader+T.
func HandleTapePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.TapePrefixActive = false
	o.PrefixActive = false
	return runPrefix(msg, o, (*config.KeybindRegistry).GetTapePrefixAction)
}

// isCtrlP reports whether a key press is Ctrl+P, regardless of how the terminal
// encoded it. The command palette binding must fire under the legacy control
// byte (0x10) and under every Kitty keyboard protocol variant a terminal might
// send. Matching on msg.String() is fragile: with associated-text reporting the
// stringified key is "p", and with alternate-key reporting it is "ctrl+P", so a
// raw-string comparison against "ctrl+p" silently misses and the key falls
// through to the shell (in fish, Ctrl+P is history-back). The decoded key event
// is stable across all of them: the code is 'p' and the only modifier is Ctrl.
// Requiring Mod == ModCtrl exactly means a bare 'p' can never match, so ordinary
// typing into the shell is untouched.
func isCtrlP(msg tea.KeyPressMsg) bool {
	return msg.Mod == tea.ModCtrl && (msg.Code == 'p' || msg.Code == 'P')
}

// handleTerminalModeBinds dispatches the direct (prefix-less) binds from the
// [keybindings.terminal_mode] section, plus the handful of main-section actions
// that must keep working while typing into a shell. It reports whether the key
// was consumed.
func handleTerminalModeBinds(msg tea.KeyPressMsg, o *app.OS) bool {
	if o.KeybindRegistry == nil {
		return false
	}
	key := msg.String()

	if _, _, ok := dispatchAction(o.KeybindRegistry.GetTerminalModeAction(key), msg, o); ok {
		return true
	}

	// Workspace switching is bound in the main section and has to work from
	// terminal mode too, but that section also binds plain letters that belong
	// to the shell. Only reserved chords (a real Alt/Ctrl modifier, or a macOS
	// Option glyph, which arrives with no modifier at all) are eligible, so
	// rebinding a workspace onto a bare letter cannot start swallowing input.
	if !isReservedTerminalChord(msg) {
		return false
	}
	action := o.KeybindRegistry.GetAction(key)
	if !isTerminalSafeAction(action) {
		return false
	}
	_, _, ok := dispatchAction(action, msg, o)
	return ok
}

// isReservedTerminalChord reports whether a key press is a chord the shell will
// never want as literal input.
func isReservedTerminalChord(msg tea.KeyPressMsg) bool {
	if msg.Mod&(tea.ModAlt|tea.ModCtrl) != 0 {
		return true
	}
	// macOS delivers Option chords as their composed glyph with no modifier
	// set. Those glyphs are ordinary typed characters on other layouts, so this
	// only applies on darwin.
	if !runtimeIsDarwin() {
		return false
	}
	key := msg.String()
	if key == "" {
		return false
	}
	first := []rune(key)[0]
	if _, ok := IsMacOSOptionKey(first); ok {
		return true
	}
	if _, ok := IsMacOSOptionShiftKey(first); ok {
		return true
	}
	return IsMacOSOptionTab(first) != ""
}

// isTerminalSafeAction reports whether a main-section action may be triggered
// from terminal mode. Only workspace movement qualifies: everything else in
// that section is a window-management verb whose keys must reach the shell.
func isTerminalSafeAction(action string) bool {
	return strings.HasPrefix(action, "switch_workspace_") ||
		strings.HasPrefix(action, "move_and_follow_")
}

// forwardKeyToFocusedWindow sends a key to the focused window's PTY, using CSI u
// encoding when the client has the kitty keyboard protocol enabled.
func forwardKeyToFocusedWindow(msg tea.KeyPressMsg, o *app.OS) {
	focused := o.GetFocusedWindow()
	if focused == nil {
		return
	}

	var rawInput []byte
	if focused.Terminal != nil && focused.Terminal.KittyKeyboardFlags() != 0 {
		if encoded := vt.EncodeKeyCSIu(vtKeyFromBubbletea(msg), focused.Terminal.KittyKeyboardFlags()); encoded != "" {
			rawInput = []byte(encoded)
		}
	}
	if len(rawInput) == 0 {
		appCursorKeys := false
		if focused.Terminal != nil {
			appCursorKeys = focused.Terminal.ApplicationCursorKeys()
		}
		rawInput = getRawKeyBytesWithMode(msg, appCursorKeys)
	}
	if len(rawInput) > 0 {
		_ = focused.SendInput(rawInput)
	}
}
