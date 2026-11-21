package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// HandleTerminalModeKey handles keyboard input in terminal mode
func HandleTerminalModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	if out, cmd, ok := routeOverlayKey(msg, o); ok {
		return out, cmd
	}

	if o.ShowCacheStats {
		return handleCacheStatsKey(msg, o)
	}

	if focusedWindow != nil && keyScrollsPane(focusedWindow) && !o.Locked() {
		action := sectionAction(msg, o, config.TableTerminalMode)
		if handleTerminalScrollBinding(action, o, focusedWindow) {
			return o, nil
		}
	}

	if focusedWindow != nil && focusedWindow.InImplicitCopyMode() && !remoteKeyBypassesCopyMode(o) {
		focusedWindow.ExitCopyMode()
		if msg.String() == "esc" {
			return o, nil
		}
	}

	o.SettleMultiCopy()

	if focusedWindow.InCopyMode() && !remoteKeyBypassesCopyMode(o) {
		return HandleCopyModeKey(msg, o, focusedWindow)
	}

	if o.ShowScrollbackBrowser {
		return HandleScrollbackBrowserKey(msg, o)
	}

	if o.Nested() {
		if isNestExitKey(msg, &o.Settings, o.HostBaseCode(msg)) {
			recordTerminalKey(o, msg)
			o.LeaveNest()
			return o, nil
		}
		return typeIntoPane(msg, o, false)
	}

	if isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) && (o.LockHeld || !leaderBoundInPrefix(msg, o)) {
		leaderWhileHeld(o)
		if o.PrefixActive {
			o.SetMenuState(app.MenuState{})
			if o.KeybindRegistry != nil && o.KeybindRegistry.LeaderTwiceGoesBack() {
				o.LastWorkspace()
				return o, nil
			}
			forwardKeyToFocusedWindow(msg, o)
			return o, nil
		}
		o.ArmLeader()
		return o, nil
	}

	if out, cmd, ok := routeMenuKey(msg, o); ok {
		return out, cmd
	}

	if o.PrefixActive {
		return HandlePrefixCommand(msg, o)
	}

	if o.Locked() {
		o.LockHeld = false
		return typeIntoPane(msg, o, false)
	}

	if cmd, ok := runTerminalModeBinds(msg, o); ok {
		return o, cmd
	}
	terminalAction := sectionAction(msg, o, config.TableTerminalMode)
	nvimNavigation := false
	if dir, ok := terminalFocusDirection(terminalAction); ok {
		nvimNavigation = o.NvimNavigationForwarding(dir)
	}

	if m, cmd, ok := handleGlobalBinds(msg, o); ok {
		return m, cmd
	}

	if terminalAction == terminalPasteHost {
		o.NoteAction(terminalPasteHost)
		if focusedWindow != nil {
			return o, o.RequestPaste()
		}
		return o, nil
	}
	return typeIntoPane(msg, o, nvimNavigation)
}

func typeIntoPane(msg tea.KeyPressMsg, o *app.OS, nvimNavigation bool) (*app.OS, tea.Cmd) {
	if typeIntoRailPane(msg, o) {
		return o, nil
	}
	focusedWindow := o.GetFocusedWindow()
	if o.DrawsTML(focusedWindow) {
		recordTerminalKey(o, msg)
		o.NotePaneKey()
		return o, o.TMLKey(focusedWindow, msg.String())
	}
	if focusedWindow != nil {
		host := paneMsg(msg, o)
		rawInput, releasedNow := paneKeyBytes(host, focusedWindow, o, true)

		if len(rawInput) > 0 {
			recordTerminalKey(o, msg)
			o.NotePaneKey()
			if !releasedNow {
				o.NotePaneKeyDown(msg.Code, focusedWindow.ID)
			}
			if err := focusedWindow.SendInput(rawInput); err != nil {
				o.ChooseMode(app.WindowManagementMode, app.ModeCauseNoPane)
				focusedWindow.InvalidateCache()
			}
			if !nvimNavigation {
				for _, w := range o.MultifocusPeers() {
					if peer, _ := paneKeyBytes(host, w, o, false); len(peer) > 0 {
						_ = w.SendInput(peer)
					}
				}
			}
		}
	} else {
		o.ChooseMode(app.WindowManagementMode, app.ModeCauseNoPane)
	}
	return o, nil
}

func typeIntoRailPane(msg tea.KeyPressMsg, o *app.OS) bool {
	if !o.SidebarSessionFocused() {
		return false
	}
	if w := o.SidebarSessionWindow(); w != nil {
		raw, released := paneKeyBytes(paneMsg(msg, o), w, o, true)
		if len(raw) > 0 && o.WriteSidebarSession(raw) && !released {
			o.NotePaneKeyDown(msg.Code, w.ID)
		}
	}
	return true
}

func recordTerminalKey(o *app.OS, msg tea.KeyPressMsg) {
	if o.TapeRecorder == nil || !o.TapeRecorder.IsRecording() || o.ShowTapeManager {
		return
	}
	keyStr := msg.String()
	if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] < 127 {
		o.TapeRecorder.RecordType(keyStr)
	} else {
		o.TapeRecorder.RecordKey(keyStr)
	}
}

func handleTerminalLayoutPrefix(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return runPrefix(msg, o, config.TableLayoutPrefix)
}

func handleLayoutPrefixLoad(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	return o, o.OpenLayoutPicker()
}

func handleLayoutPrefixSave(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenLayoutSave()
	return o, nil
}

func keyScrollsPane(w *terminal.Window) bool {
	if w.InCopyMode() {
		return true
	}
	if w.IsAltScreen() {
		return false
	}
	return w.Terminal == nil || !w.Terminal.HasMouseMode()
}
