package input

import (
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// The key handlers below are shared by terminal mode and window-management
// mode. Each mode used to carry its own copy, and the copies drifted: the help
// overlay closed on different keys depending on which mode it had been opened
// from. Each mode still calls them at its own point in its routing order.

// routeSettingsOverlayKey sends the key to the settings overlay, or to one of
// the pickers and editors that open over it, or to the layout or machine
// picker, when one of them is open. It reports whether an overlay took the key.
//
// Both modes check these overlays first and in this order. The overlays after
// them (palette, launcher, switchers, mail, aggregate view) are checked in a
// different order in each mode, and several panels can be open at once, so
// those stay in the mode handlers.
func routeSettingsOverlayKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	var out *app.OS
	var cmd tea.Cmd
	switch {
	case o.ShowThemePicker:
		out, cmd = handleThemePickerInput(msg, o)
	case o.ShowGlyphPicker:
		out, cmd = handleGlyphPickerInput(msg, o)
	case o.ShowEffectPicker:
		out, cmd = handleEffectPickerInput(msg, o)
	case o.ShowSectionEditor:
		out, cmd = handleSectionEditorInput(msg, o)
	case o.ShowDockEditor:
		out, cmd = handleDockEditorInput(msg, o)
	case o.ShowKeybindManager:
		// Opens over settings, like the theme picker.
		out, cmd = handleKeybindManagerInput(msg, o)
	case o.ShowSettings:
		out, cmd = handleSettingsInput(msg, o)
	case o.ShowLayoutPicker:
		out, cmd = handleLayoutPickerInput(msg, o)
	case o.ShowHostPicker:
		// A letter typed into the machine picker is a search and must not
		// reach the shell or a binding.
		out, cmd = handleHostPickerInput(msg, o)
	default:
		return o, nil, false
	}
	return out, cmd, true
}

// closeHelp closes the help overlay and resets its view, so it opens on the
// auto-selected category with no search next time.
func closeHelp(o *app.OS) {
	o.ShowHelp = false
	o.HelpScrollOffset = 0
	o.HelpCategory = -1
	o.HelpSearchQuery = ""
	o.HelpSearchMode = false
}

// handleHelpOverlayKey handles a key while the help overlay is open. Help is
// modal: a key it does not use is swallowed rather than passed on to a binding
// or the pane behind it.
//
// The close rule, which is what the footer hints advertise:
//   - esc leaves search when searching, and closes help otherwise.
//   - ? closes help, searching or not.
//   - q closes help when not searching, and is typed into the query when
//     searching, so a search can contain it.
func handleHelpOverlayKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		if o.HelpSearchMode {
			o.HelpSearchMode = false
			o.HelpSearchQuery = ""
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
		// Scroll by 2 rows at a time (1 entry + 1 gap row).
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
		o.HelpScrollOffset = 0 // Reset scroll when changing categories
		return handleLeftKey(msg, o)
	case "right":
		o.HelpScrollOffset = 0 // Reset scroll when changing categories
		return handleRightKey(msg, o)
	case "/":
		o.HelpSearchMode = !o.HelpSearchMode
		o.HelpScrollOffset = 0 // Reset scroll when toggling search
		if !o.HelpSearchMode {
			o.HelpSearchQuery = "" // Clear query when exiting search
		}
		return o, nil
	}

	if o.HelpSearchMode {
		if key == "backspace" {
			if len(o.HelpSearchQuery) > 0 {
				_, size := utf8.DecodeLastRuneInString(o.HelpSearchQuery)
				o.HelpSearchQuery = o.HelpSearchQuery[:len(o.HelpSearchQuery)-size]
				o.HelpScrollOffset = 0 // Reset scroll when query changes
			}
			return o, nil
		}

		// Single printable characters extend the query.
		if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
			o.HelpSearchQuery += key
			o.HelpScrollOffset = 0 // Reset scroll when query changes
			return o, nil
		}
	}

	return o, nil
}

// handleCacheStatsKey handles a key while the style cache statistics overlay is
// open: q, esc or c close it, r resets the counters, and every other key is
// swallowed.
func handleCacheStatsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "c":
		o.ShowCacheStats = false
	case "r":
		app.GetGlobalStyleCache().ResetStats()
		o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
	}
	return o, nil
}
