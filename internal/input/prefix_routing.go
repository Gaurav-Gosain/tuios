package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// runPrefix closes the open sub-prefix and dispatches the key through its
// section. An unbound key is simply dropped, which is what dismissing a prefix
// chord should do. In lock mode a repeatable action keeps the sub-prefix open
// until esc (see lock_mode.go).
func runPrefix(msg tea.KeyPressMsg, o *app.OS, lookup sectionLookup) (*app.OS, tea.Cmd) {
	k := startMenuKey(o)
	if o.KeybindRegistry == nil || k.leaves(msg) {
		return o, nil
	}
	action := lookupAction(o, msg, func(key string) string { return lookup(o.KeybindRegistry, key) })
	if k.refuses(o, action) {
		k.unbound(msg, o)
		return o, nil
	}
	m, cmd, ran := dispatchAction(action, msg, o)
	if ran {
		k.ran(m, action)
	} else {
		k.unbound(msg, o)
	}
	return m, cmd
}

// HandlePrefixCommand handles the key pressed after the leader key, in either
// mode. An unbound key falls through to the terminal in terminal mode (so the
// leader key does not swallow shell input) and is ignored in window-management
// mode.
func HandlePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	k := startMenuKey(o)
	if k.leaves(msg) {
		return o, nil
	}

	if o.KeybindRegistry != nil {
		action := lookupAction(o, msg, o.KeybindRegistry.GetPrefixAction)
		if k.refuses(o, action) {
			k.unbound(msg, o)
			return o, nil
		}
		if app.PrefixWorkActions[action] {
			if cmd, handled := runPrefixWork(action, o); handled {
				if !k.ran(o, action) {
					armIfRepeatable(o, action)
				}
				return o, cmd
			}
			// Not built yet: the key is treated as unbound below.
			o.ClearPrefixRepeat()
			action = ""
		}
		if m, cmd, ok := dispatchAction(action, msg, o); ok {
			if !k.ran(m, action) {
				armIfRepeatable(m, action)
			}
			return m, cmd
		}
	}

	// ctrl+c cancels a prefix everywhere: the way out when a chord was started by
	// accident. Checked after the prefix lookup above, so a ctrl+c the user bound
	// to a prefix action still runs; what cannot be taken away is the escape.
	if msg.String() == "ctrl+c" {
		return o, nil
	}

	if o.Mode == app.TerminalMode {
		// The leader asked the host for every key as an escape code. A
		// non-ASCII key that still came as bare text beat that switch, so its
		// base-layout key is missing and the key it was meant as is unknown.
		// It was typed after the leader, so it is dropped, not typed into the
		// pane, and the prefix stays pending so the key can be typed again.
		if o.AllKeysPending() && isLayoutTextWithoutBase(msg) {
			o.PrefixActive = true
			return o, nil
		}
		forwardKeyToFocusedWindow(msg, o)
	}
	return o, nil
}

// runPrefixWork runs one of the review and triage prefix actions, which say
// whether they did anything. Only one that did is recorded as run, the way
// the Inbox's own work keys are, so until its work lands the key is exactly
// the unbound key it was before: forwarded to the pane in terminal mode.
func runPrefixWork(action string, o *app.OS) (tea.Cmd, bool) {
	cmd, handled := o.PrefixWorkAction(action)
	if handled {
		o.NoteAction(action)
		if o.OnAction != nil {
			o.OnAction(action)
		}
	}
	return cmd, handled
}

// HandleWorkspacePrefixCommand handles the key after leader+w.
func HandleWorkspacePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, (*config.KeybindRegistry).GetWorkspacePrefixAction)
}

// HandleMinimizePrefixCommand handles the key after leader+m.
func HandleMinimizePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, (*config.KeybindRegistry).GetMinimizePrefixAction)
}

// HandleTilingPrefixCommand handles the key after leader+t (the window prefix).
func HandleTilingPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, (*config.KeybindRegistry).GetWindowPrefixAction)
}

// HandleDebugPrefixCommand handles the key after leader+D.
func HandleDebugPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, (*config.KeybindRegistry).GetDebugPrefixAction)
}

// HandleTapePrefixCommand handles the key after leader+T.
func HandleTapePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, (*config.KeybindRegistry).GetTapePrefixAction)
}

// HandleTuiosPrefixCommand handles the key in the tuios menu. A key there may
// open another menu (leader ctrl+t m is the minimize menu under the tmux
// preset): the action sets that menu's flag as it does from the leader.
func HandleTuiosPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, (*config.KeybindRegistry).GetTuiosPrefixAction)
}

// handleTerminalModeBinds dispatches the direct (prefix-less) binds from the
// [keybindings.terminal_mode] section, plus the handful of main-section actions
// that must keep working while typing into a shell. It reports whether the key
// was consumed.
func handleTerminalModeBinds(msg tea.KeyPressMsg, o *app.OS) bool {
	_, ok := runTerminalModeBinds(msg, o)
	return ok
}

// runTerminalModeBinds is handleTerminalModeBinds with the action's command,
// which the caller has to return: move_workspace_left/right do their work in
// it, and dropping it left the move in flight forever (#1217).
func runTerminalModeBinds(msg tea.KeyPressMsg, o *app.OS) (tea.Cmd, bool) {
	if o.KeybindRegistry == nil {
		return nil, false
	}
	action := lookupAction(o, msg, o.KeybindRegistry.GetTerminalModeAction)
	if o.Mode == app.TerminalMode && isTerminalFocusAction(action) && o.ArmNvimNavigation(nvimNavigationDirection(action)) {
		return nil, false
	}
	if _, cmd, ok := dispatchAction(action, msg, o); ok {
		return cmd, true
	}

	// Workspace switching is bound in the main section and has to work from
	// terminal mode too, but that section also binds plain letters that belong
	// to the shell. Only reserved chords (a real Alt/Ctrl modifier, or a macOS
	// Option glyph, which arrives with no modifier at all) are eligible, so
	// rebinding a workspace onto a bare letter cannot start swallowing input.
	if !isReservedTerminalChord(msg) {
		return nil, false
	}
	action = lookupAction(o, msg, o.KeybindRegistry.GetAction)
	if !isTerminalSafeAction(action) {
		return nil, false
	}
	_, cmd, ok := dispatchAction(action, msg, o)
	return cmd, ok
}

func isTerminalFocusAction(action string) bool {
	return action == "terminal_focus_left" || action == "terminal_focus_right" ||
		action == "terminal_focus_up" || action == "terminal_focus_down"
}

func nvimNavigationDirection(action string) string {
	return strings.TrimPrefix(action, "terminal_focus_")
}

// isReservedTerminalChord reports whether a key press is a chord the shell will
// never want as literal input.
func isReservedTerminalChord(msg tea.KeyPressMsg) bool {
	if msg.Mod&(tea.ModAlt|tea.ModCtrl) != 0 {
		return true
	}
	// macOS delivers Option chords as their composed glyph with no modifier
	// set. Those glyphs are ordinary typed characters on other layouts, so this
	// only applies on darwin (macOptionChord enforces that).
	_, ok := macOptionChord(msg)
	return ok
}

// isTerminalSafeAction reports whether a main-section action may be triggered
// from terminal mode. Only navigation between workspaces, sessions and agent
// panes qualifies:
// everything else in that section is a window-management verb whose keys must
// reach the shell.
func isTerminalSafeAction(action string) bool {
	return strings.HasPrefix(action, "switch_workspace_") ||
		strings.HasPrefix(action, "move_and_follow_") ||
		strings.HasPrefix(action, "send_to_workspace_") ||
		strings.HasPrefix(action, "switch_session_") ||
		action == "next_workspace" || action == "prev_workspace" ||
		action == "next_session" || action == "prev_session" ||
		action == "next_host" || action == "prev_host" ||
		action == "next_agent" || action == "prev_agent"
}

// forwardKeyToFocusedWindow sends a key to the focused window's PTY, using CSI u
// encoding when the client has the kitty keyboard protocol enabled.
func forwardKeyToFocusedWindow(msg tea.KeyPressMsg, o *app.OS) {
	focused := o.GetFocusedWindow()
	if focused == nil {
		return
	}

	// The pane gets the key as the host sent it, on either encoding, with its
	// release when the host will send none (see paneKeyBytes).
	rawInput, released := paneKeyBytes(paneMsg(msg, o), focused, o, true)
	if len(rawInput) > 0 {
		if !released {
			o.NotePaneKeyDown(msg.Code, focused.ID)
		}
		_ = focused.SendInput(rawInput)
	}
}
