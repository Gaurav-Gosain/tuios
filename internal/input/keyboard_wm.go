package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func remoteKeyBypassesCopyMode(o *app.OS) bool {
	return o.ProcessingRemoteKeys
}

// HandleWindowManagementModeKey handles keyboard input in window management mode
func HandleWindowManagementModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	if focusedWindow != nil && focusedWindow.InImplicitCopyMode() && !remoteKeyBypassesCopyMode(o) {
		focusedWindow.ExitCopyMode()
	}

	o.SettleMultiCopy()

	if focusedWindow.InCopyMode() && !remoteKeyBypassesCopyMode(o) {
		return HandleCopyModeKey(msg, o, focusedWindow)
	}

	if o.ShowScrollbackBrowser {
		return HandleScrollbackBrowserKey(msg, o)
	}

	if out, cmd, ok := routeOverlayKey(msg, o); ok {
		return out, cmd
	}

	key := msg.String()

	if cmd, ok := o.WelcomeChoiceKey(key); ok {
		return o, cmd
	}

	if o.ShowCacheStats {
		return handleCacheStatsKey(msg, o)
	}

	if key == "esc" && o.FocusedPopup() != nil {
		action := ""
		if o.KeybindRegistry != nil {
			action = lookupAction(o, msg, o.KeybindRegistry.GetAction)
		}
		if action == "" || action == "enter_window_mode" {
			o.CloseFocusedPopup()
			return o, nil
		}
	}

	if key == "esc" && o.SpotlightOn() {
		action := ""
		if o.KeybindRegistry != nil {
			action = lookupAction(o, msg, o.KeybindRegistry.GetAction)
		}
		if action == "" || action == "enter_window_mode" {
			save, _ := o.TurnOffSpotlight()
			return o, save
		}
	}

	if o.KeybindRegistry != nil {
		action := lookupAction(o, msg, o.KeybindRegistry.GetAction)
		if action != "" {
			dispatcher := GetDispatcher()
			if dispatcher.HasAction(action) {
				return dispatcher.Dispatch(action, msg, o)
			}
		}
	}

	if cmd, ok := runTerminalModeBinds(msg, o); ok {
		return o, cmd
	}

	if m, cmd, ok := handleGlobalBinds(msg, o); ok {
		return m, cmd
	}

	switch key {
	case "ctrl+c":
		return requestQuit(o)

	default:
		return o, nil
	}
}
