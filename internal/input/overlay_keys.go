package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func routeSettingsOverlayKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	var out *app.OS
	var cmd tea.Cmd
	switch {
	case o.ThemeEditorOpen():
		out, cmd = handleThemeEditorInput(msg, o)
	case o.ShowThemePicker:
		out, cmd = handleThemePickerInput(msg, o)
	case o.PresetPickerOpen():
		out, cmd = handlePresetPickerInput(msg, o)
	case o.ShowGlyphPicker:
		out, cmd = handleGlyphPickerInput(msg, o)
	case o.ShowEffectPicker:
		out, cmd = handleEffectPickerInput(msg, o)
	case o.ShowSectionEditor:
		out, cmd = handleSectionEditorInput(msg, o)
	case o.ShowDockEditor:
		out, cmd = handleDockEditorInput(msg, o)
	case o.ShowKeybindManager:
		out, cmd = handleKeybindManagerInput(msg, o)
	case o.ShowSettings:
		out, cmd = handleSettingsInput(msg, o)
	case o.ShowLayoutPicker:
		out, cmd = handleLayoutPickerInput(msg, o)
	case o.ShowHostPicker:
		out, cmd = handleHostPickerInput(msg, o)
	default:
		return o, nil, false
	}
	return out, cmd, true
}

func closeHelp(o *app.OS) {
	o.ShowHelp = false
	o.HelpScrollOffset = 0
	o.HelpCategory = -1
	o.SetHelpSearchQuery("")
	o.HelpSearchMode = false
}

func handleHelpOverlayKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if !o.HelpSearchMode {
		key = commandKey(msg)
	}

	switch key {
	case "esc":
		if o.HelpSearchMode {
			o.HelpSearchMode = false
			o.SetHelpSearchQuery("")
			o.HelpScrollOffset = 0
			return o, nil
		}
		closeHelp(o)
		return o, nil
	case "?":
		closeHelp(o)
		return o, nil
	case "q":
		if !o.HelpSearchMode {
			closeHelp(o)
			return o, nil
		}
	case "up":
		if o.HelpScrollOffset > 0 {
			o.HelpScrollOffset -= 2
			if o.HelpScrollOffset < 0 {
				o.HelpScrollOffset = 0
			}
		}
		return o, nil
	case "down":
		o.HelpScrollOffset += 2
		return o, nil
	case "left":
		o.HelpScrollOffset = 0
		return handleLeftKey(msg, o)
	case "right":
		o.HelpScrollOffset = 0
		return handleRightKey(msg, o)
	case "/":
		o.HelpSearchMode = !o.HelpSearchMode
		o.HelpScrollOffset = 0
		if !o.HelpSearchMode {
			o.SetHelpSearchQuery("")
		}
		return o, nil
	}

	if o.HelpSearchMode {
		if changed, consumed := o.HelpSearchKey(msg.String(), msg.Text); consumed {
			if changed {
				o.HelpScrollOffset = 0
			}
			return o, nil
		}
	}

	return o, nil
}

func handleCacheStatsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch commandKey(msg) {
	case "q", "esc", "c":
		o.ShowCacheStats = false
	case "r":
		app.GetGlobalStyleCache().ResetStats()
		o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
	}
	return o, nil
}

func routeOverlayKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !o.AnyOverlayOpen() {
		return o, nil, false
	}
	stack := o.OverlayStack()
	for i := len(stack) - 1; i >= 0; i-- {
		if out, cmd, ok := overlayKey(stack[i], msg, o); ok {
			return out, cmd, true
		}
	}
	return o, nil, false
}

func overlayKey(kind string, msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	var out *app.OS
	var cmd tea.Cmd
	switch kind {
	case "help":
		out, cmd = handleHelpOverlayKey(msg, o)
	case "settings", "keybinds", "themepicker", "themeeditor", "presetpicker", "glyphpicker",
		"effectpicker", "dockeditor", "sectioneditor", "layout", "hostpicker":
		return routeSettingsOverlayKey(msg, o)
	case "palette":
		out, cmd = handleCommandPaletteInput(msg, o)
	case "launcher":
		out, cmd = handleLauncherInput(msg, o)
	case "agentmail":
		out, cmd = handleAgentMailInput(msg, o)
	case "inbox":
		if o.PrefixActive || isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
			return o, nil, false
		}
		out, cmd = handleInboxInput(msg, o)
	case "navigator":
		out, cmd = handleNavigatorInput(msg, o)
	case "goto":
		out, cmd = handleGotoPickerInput(msg, o)
	case "buffers":
		out, cmd = handleBufferChooserInput(msg, o)
	case "message":
		if !viewerTakesKey(msg, o) {
			return o, nil, false
		}
		out, cmd = handleMessageViewKey(msg, o)
	case "logs":
		if !viewerTakesKey(msg, o) {
			return o, nil, false
		}
		out, cmd = handleLogViewerKey(msg, o)
	default:
		return o, nil, false
	}
	return out, cmd, true
}
