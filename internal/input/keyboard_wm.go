package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// remoteKeyBypassesCopyMode reports whether the key being handled came from a
// remote sender (tuios send-keys, or a tape run from outside) rather than
// from the person at this client.
//
// Copy mode, implicit or explicit, is that person's viewport: a scrolled view
// they are reading, or a selection they are making. Their own keystroke ends
// an implicit session because they have stopped reading, and drives an
// explicit one because they asked for it. A remote key is neither. It goes
// where it would go with no copy mode in progress (to the guest, or to the
// binding it names), and the viewport stays where the person put it.
// Otherwise an agent typing into the pane while the person reads its earlier
// output would return the view to the bottom at a moment decided by another
// process.
//
// The flag is set for the whole run of a remote key sequence or tape and
// cleared when it is done, on the Update goroutine that dispatches both.
func remoteKeyBypassesCopyMode(o *app.OS) bool {
	return o.ProcessingRemoteKeys
}

// HandleWindowManagementModeKey handles keyboard input in window management mode
func HandleWindowManagementModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	// A wheel scroll here leaves the pane in an implicit copy mode too, and a
	// window-manager binding must not be swallowed by it. Any key the person
	// presses ends the scrolled view and is then handled as the window-manager
	// command it is. A remote key is not the person's: it leaves the view
	// alone and is handled as the command it is. See remoteKeyBypassesCopyMode.
	if focusedWindow != nil && focusedWindow.InImplicitCopyMode() && !remoteKeyBypassesCopyMode(o) {
		focusedWindow.ExitCopyMode()
	}

	// Handle copy mode (vim-style scrollback/selection), which takes priority
	if focusedWindow.InCopyMode() && !remoteKeyBypassesCopyMode(o) {
		return HandleCopyModeKey(msg, o, focusedWindow)
	}

	// Handle scrollback browser overlay
	if o.ShowScrollbackBrowser {
		return HandleScrollbackBrowserKey(msg, o)
	}

	// Settings, the pickers over it, and the layout and machine pickers.
	if out, cmd, ok := routeSettingsOverlayKey(msg, o); ok {
		return out, cmd
	}

	// Handle command palette overlay
	if o.ShowCommandPalette {
		return handleCommandPaletteInput(msg, o)
	}

	// Handle launcher overlay
	if o.ShowLauncher {
		return handleLauncherInput(msg, o)
	}

	// Handle session switcher overlay
	if o.ShowSessionSwitcher {
		return handleSessionSwitcherInput(msg, o)
	}

	// The mailbox owns the keyboard while it is up, like the switcher.
	if o.ShowAgentMail {
		return handleAgentMailInput(msg, o)
	}

	// Handle workspace switcher overlay
	if o.ShowWorkspaceSwitcher {
		return handleWorkspaceSwitcherInput(msg, o)
	}

	// Handle aggregate view overlay
	if o.ShowAggregateView {
		return handleAggregateViewInput(msg, o)
	}

	key := msg.String()

	// Handle help menu interactions before general keybind dispatch. Help is
	// modal, exactly as in terminal mode: an unhandled key used to fall through
	// to the window-manager dispatch below, where the overlay hid what it did.
	// n created and focused a window behind the help panel, x closed one, t
	// toggled tiling, and every other single-letter binding fired unseen.
	if o.ShowHelp {
		return handleHelpOverlayKey(msg, o)
	}

	// Handle log viewer (takes priority in window management mode)
	if o.ShowLogs {
		return handleLogViewerKey(msg, o)
	}

	// Handle cache stats viewer (takes priority in window management mode)
	if o.ShowCacheStats {
		return handleCacheStatsKey(msg, o)
	}

	// Esc closes the focused popup.
	//
	// It reaches this function only in window mode, because the pane owns esc in
	// terminal mode: fzf, gum and vim all quit on it, and taking it from them
	// would break the programs a popup exists to run. So closing a popup from
	// the keyboard is esc to leave the pane, then esc to close the box. A popup
	// whose command exits closes itself and needs neither.
	//
	// It runs before the registry dispatch because esc is bound to
	// enter_window_mode, which is what already has the key here and does nothing
	// with it: this is window mode. So nothing is taken from the user, and to
	// keep that true for a user who bound esc to something of their own, the
	// claim is made only while that default binding is the one in force.
	if key == "esc" && o.FocusedPopup() != nil {
		action := ""
		if o.KeybindRegistry != nil {
			action = lookupAction(msg, o.KeybindRegistry.GetAction)
		}
		if action == "" || action == "enter_window_mode" {
			o.CloseFocusedPopup()
			return o, nil
		}
	}

	// Try config-based dispatch first (if registry is available)
	if o.KeybindRegistry != nil {
		action := lookupAction(msg, o.KeybindRegistry.GetAction)
		if action != "" {
			dispatcher := GetDispatcher()
			if dispatcher.HasAction(action) {
				return dispatcher.Dispatch(action, msg, o)
			}
		}
	}

	// The direct terminal-mode binds (window cycling) are honoured here too, so
	// the same chord cycles windows in both modes.
	if handleTerminalModeBinds(msg, o) {
		return o, nil
	}

	// The global scope (palette, launcher) acts here and in terminal mode alike.
	if m, cmd, ok := handleGlobalBinds(msg, o); ok {
		return m, cmd
	}

	// Ctrl+C is the last resort, and it is a fallback rather than an override:
	// the registry dispatch above has already had the key, so a user who binds
	// ctrl+c to something of their own gets it. What is not configurable is
	// ctrl+c doing nothing, which is the point of keeping it.
	switch key {
	case "ctrl+c":
		// Same routing as the quit keybinding, so in a daemon session it opens
		// the quit menu (detach is the default) rather than silently killing
		// anything.
		return requestQuit(o)

	default:
		// All other keybindings are handled by the config system above
		// Workspace switching (opt+1-9, opt+shift+1-9) is now fully configurable
		// The KeyNormalizer handles macOS unicode character expansion (¡, ™, £, etc.)
		// If a key isn't bound in the config, it does nothing (which is correct behavior)
		return o, nil
	}
}
