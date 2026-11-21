package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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

func runPrefix(msg tea.KeyPressMsg, o *app.OS, table config.Table) (*app.OS, tea.Cmd) {
	k := startMenuKey(o)
	if o.KeybindRegistry == nil || k.leaves(msg) {
		return o, nil
	}
	action := tableAction(o, msg, o.KeybindRegistry, table)
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
		action := tableAction(o, msg, o.KeybindRegistry, config.TablePrefixMode)
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

	if msg.String() == "ctrl+c" {
		return o, nil
	}

	if o.Mode == app.TerminalMode {
		if o.AllKeysPending() && isLayoutTextWithoutBase(msg) {
			o.KeepLeaderPending()
			return o, nil
		}
		forwardKeyToFocusedWindow(msg, o)
	}
	return o, nil
}

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
	return runPrefix(msg, o, config.TableWorkspacePrefix)
}

// HandleMinimizePrefixCommand handles the key after leader+m.
func HandleMinimizePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, config.TableMinimizePrefix)
}

// HandleTilingPrefixCommand handles the key after leader+t (the window prefix).
func HandleTilingPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, config.TableWindowPrefix)
}

// HandleDebugPrefixCommand handles the key after leader+D.
func HandleDebugPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, config.TableDebugPrefix)
}

// HandleTapePrefixCommand handles the key after leader+T.
func HandleTapePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, config.TableTapePrefix)
}

// HandleTuiosPrefixCommand handles the key in the tuios menu. A key there may
// open another menu (leader ctrl+t m is the minimize menu under the tmux
// preset): the action sets that menu's flag as it does from the leader.
func HandleTuiosPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, config.TableTuiosPrefix)
}

func handleTerminalModeBinds(msg tea.KeyPressMsg, o *app.OS) bool {
	_, ok := runTerminalModeBinds(msg, o)
	return ok
}

func runTerminalModeBinds(msg tea.KeyPressMsg, o *app.OS) (tea.Cmd, bool) {
	if o.KeybindRegistry == nil {
		return nil, false
	}
	action := tableAction(o, msg, o.KeybindRegistry, config.TableTerminalMode)
	if dir, ok := terminalFocusDirection(action); ok && o.Mode == app.TerminalMode && o.ArmNvimNavigation(dir) {
		return nil, false
	}
	if _, cmd, ok := dispatchAction(action, msg, o); ok {
		return cmd, true
	}

	if !isReservedTerminalChord(msg) {
		return nil, false
	}
	action = lookupAction(o, msg, o.KeybindRegistry.GetAction)
	if !config.TerminalSafeAction(action) {
		return nil, false
	}
	_, cmd, ok := dispatchAction(action, msg, o)
	return cmd, ok
}

func terminalFocusDirection(action string) (string, bool) {
	switch config.CanonicalAction(action) {
	case "terminal_focus_left":
		return "left", true
	case "terminal_focus_right":
		return "right", true
	case "focus_up":
		return "up", true
	case "focus_down":
		return "down", true
	}
	return "", false
}

func isTerminalFocusAction(action string) bool {
	_, ok := terminalFocusDirection(action)
	return ok
}

func isReservedTerminalChord(msg tea.KeyPressMsg) bool {
	if msg.Mod&(tea.ModAlt|tea.ModCtrl) != 0 {
		return true
	}
	_, ok := macOptionChord(msg)
	return ok
}

func forwardKeyToFocusedWindow(msg tea.KeyPressMsg, o *app.OS) {
	focused := o.GetFocusedWindow()
	if focused == nil {
		return
	}

	rawInput, released := paneKeyBytes(paneMsg(msg, o), focused, o, true)
	if len(rawInput) > 0 {
		if !released {
			o.NotePaneKeyDown(msg.Code, focused.ID)
		}
		_ = focused.SendInput(rawInput)
	}
}

var subMenuKeys = map[app.SubMenu]func(tea.KeyPressMsg, *app.OS) (*app.OS, tea.Cmd){
	app.SubMenuWorkspace: HandleWorkspacePrefixCommand,
	app.SubMenuMinimize:  HandleMinimizePrefixCommand,
	app.SubMenuTiling:    HandleTilingPrefixCommand,
	app.SubMenuDebug:     HandleDebugPrefixCommand,
	app.SubMenuTape:      HandleTapePrefixCommand,
	app.SubMenuTuios:     HandleTuiosPrefixCommand,
	app.SubMenuLayout:    handleTerminalLayoutPrefix,
}

func routeMenuKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if handle, ok := subMenuKeys[o.SubMenu]; ok {
		out, cmd := handle(msg, o)
		return out, cmd, true
	}
	if o.Menu != "" {
		out, cmd := HandleMenuCommand(msg, o)
		return out, cmd, true
	}
	return o, nil, false
}
