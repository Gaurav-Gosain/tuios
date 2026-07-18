package input

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// HandleTerminalModeKey handles keyboard input in terminal mode
func HandleTerminalModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Guard: suppress misparsed mouse-sequence fragments during AllMotion→CellMotion transition.
	// When switching from WindowManagementMode (AllMotion) to TerminalMode (CellMotion),
	// buffered mouse motion sequences can be split across read boundaries. ultraviolet's
	// 50ms ESC timeout force-processes partial CSI sequences, and the remaining bytes
	// (digits, 'M', ';', etc.) are decoded as individual KeyPressEvents.
	// Suppress unmodified single-character keys for 150ms after entering TerminalMode.
	if msg.Mod == 0 && msg.Text != "" && !o.PrefixActive && !o.TerminalModeEnteredAt.IsZero() &&
		time.Since(o.TerminalModeEnteredAt) < 150*time.Millisecond {
		return o, nil
	}

	focusedWindow := o.GetFocusedWindow()

	// Handle help menu first (takes priority over everything in terminal mode)
	if o.ShowHelp {
		key := msg.String()

		// Handle escape - exit search first if active, then close help
		if key == "esc" {
			if o.HelpSearchMode {
				// Exit search mode first
				o.HelpSearchMode = false
				o.HelpSearchQuery = ""
				o.HelpScrollOffset = 0
				return o, nil
			}
			// Close help menu
			o.ShowHelp = false
			o.HelpScrollOffset = 0
			o.HelpCategory = -1
			return o, nil
		}

		// Handle ? to close help
		if key == "?" {
			o.ShowHelp = false
			o.HelpScrollOffset = 0
			o.HelpCategory = -1 // Reset to trigger auto-selection next time
			o.HelpSearchQuery = ""
			o.HelpSearchMode = false
			return o, nil
		}

		// Handle up/down arrows for scrolling
		// Scroll by 2 rows at a time (1 entry + 1 gap row)
		if key == "up" {
			if o.HelpScrollOffset > 0 {
				o.HelpScrollOffset -= 2
				if o.HelpScrollOffset < 0 {
					o.HelpScrollOffset = 0
				}
			}
			return o, nil
		}
		if key == "down" {
			o.HelpScrollOffset += 2
			return o, nil
		}

		// Handle left/right arrows for category navigation
		if key == "left" {
			o.HelpScrollOffset = 0 // Reset scroll when changing categories
			return handleLeftKey(msg, o)
		}
		if key == "right" {
			o.HelpScrollOffset = 0 // Reset scroll when changing categories
			return handleRightKey(msg, o)
		}

		// Toggle search mode with "/"
		if key == "/" {
			o.HelpSearchMode = !o.HelpSearchMode
			o.HelpScrollOffset = 0 // Reset scroll when toggling search
			if !o.HelpSearchMode {
				o.HelpSearchQuery = "" // Clear query when exiting search
			}
			return o, nil
		}

		// Handle typing in search mode
		if o.HelpSearchMode {
			// Handle backspace
			if key == "backspace" {
				if len(o.HelpSearchQuery) > 0 {
					o.HelpSearchQuery = o.HelpSearchQuery[:len(o.HelpSearchQuery)-1]
					o.HelpScrollOffset = 0 // Reset scroll when query changes
				}
				return o, nil
			}

			// Handle regular character input (single printable characters)
			if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
				o.HelpSearchQuery += key
				o.HelpScrollOffset = 0 // Reset scroll when query changes
				return o, nil
			}
		}

		// Help is showing but key wasn't handled - ignore it
		return o, nil
	}

	// Handle theme picker overlay (opens on top of settings)
	if o.ShowThemePicker {
		return handleThemePickerInput(msg, o)
	}

	// Handle settings overlay (takes priority in terminal mode)
	if o.ShowSettings {
		return handleSettingsInput(msg, o)
	}

	// Handle layout picker (takes priority in terminal mode)
	if o.ShowLayoutPicker {
		return handleLayoutPickerInput(msg, o)
	}

	// Handle session switcher (takes priority in terminal mode)
	if o.ShowSessionSwitcher {
		return handleSessionSwitcherInput(msg, o)
	}

	// Handle aggregate view
	if o.ShowAggregateView {
		return handleAggregateViewInput(msg, o)
	}

	// Handle command palette (takes priority in terminal mode)
	if o.ShowCommandPalette {
		return handleCommandPaletteInput(msg, o)
	}

	// Handle log viewer (takes priority in terminal mode)
	if o.ShowLogs {
		return handleLogViewerKey(msg, o)
	}

	// Handle cache stats viewer (takes priority in terminal mode)
	if o.ShowCacheStats {
		key := msg.String()

		// Close cache stats with q, esc, or c
		if key == "q" || key == "esc" || key == "c" {
			o.ShowCacheStats = false
			return o, nil
		}

		// Reset cache stats with r
		if key == "r" {
			app.GetGlobalStyleCache().ResetStats()
			o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
			return o, nil
		}

		// Ignore other keys when cache stats is active
		return o, nil
	}

	// Shift+Up/Shift+Down: enter copy mode (if not active) and scroll scrollback.
	// Handled BEFORE the copy mode check so subsequent presses also scroll
	// instead of being consumed by the copy mode key handler.
	if focusedWindow != nil {
		shiftScroll := msg.String()
		if shiftScroll == "shift+up" || shiftScroll == "shift+down" {
			if focusedWindow.CopyMode == nil || !focusedWindow.CopyMode.Active {
				focusedWindow.EnterCopyMode()
			}
			if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
				cm := focusedWindow.CopyMode
				if shiftScroll == "shift+up" {
					if cm.ScrollOffset < focusedWindow.ScrollbackLen() {
						cm.ScrollOffset++
						focusedWindow.ScrollbackOffset = cm.ScrollOffset
					}
				} else {
					if cm.ScrollOffset > 0 {
						cm.ScrollOffset--
						focusedWindow.ScrollbackOffset = cm.ScrollOffset
					}
				}
				focusedWindow.InvalidateCache()
			}
			return o, nil
		}
	}

	// Handle copy mode (vim-style scrollback/selection)
	if focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
		return HandleCopyModeKey(msg, o, focusedWindow)
	}

	// Handle scrollback browser overlay
	if o.ShowScrollbackBrowser {
		return HandleScrollbackBrowserKey(msg, o)
	}

	// Check for prefix key in terminal mode
	msgStr := strings.ToLower(msg.String())
	leaderKey := strings.ToLower(config.LeaderKey)
	if msgStr == leaderKey {
		// If prefix is already active, send the leader key to terminal
		if o.PrefixActive {
			o.PrefixActive = false
			if focusedWindow != nil {
				// Use CSI u encoding if kitty keyboard is active
				if focusedWindow.Terminal != nil && focusedWindow.Terminal.KittyKeyboardFlags() != 0 {
					encoded := vt.EncodeKeyCSIu(vtKeyFromBubbletea(msg), focusedWindow.Terminal.KittyKeyboardFlags())
					if len(encoded) > 0 {
						_ = focusedWindow.SendInput([]byte(encoded))
						return o, nil
					}
				}
				// Legacy: send raw Ctrl+B byte
				_ = focusedWindow.SendInput([]byte{0x02})
			}
			return o, nil
		}
		// Activate prefix mode
		o.PrefixActive = true
		o.LastPrefixTime = time.Now()
		return o, nil
	}

	// Handle workspace prefix commands (Ctrl+B, w, ...)
	if o.WorkspacePrefixActive {
		return HandleWorkspacePrefixCommand(msg, o)
	}

	// Handle minimize prefix commands (Ctrl+B, m, ...)
	if o.MinimizePrefixActive {
		return HandleMinimizePrefixCommand(msg, o)
	}

	// Handle tiling prefix commands (Ctrl+B, t, ...)
	if o.TilingPrefixActive {
		return HandleTilingPrefixCommand(msg, o)
	}

	// Handle debug prefix commands (Ctrl+B, D, ...)
	if o.DebugPrefixActive {
		return HandleDebugPrefixCommand(msg, o)
	}

	// Handle tape prefix commands (Ctrl+B, T, ...)
	if o.TapePrefixActive {
		return HandleTapePrefixCommand(msg, o)
	}

	// Handle layout prefix commands (Ctrl+B, L, ...)
	if o.LayoutPrefixActive {
		return handleTerminalLayoutPrefix(msg, o)
	}

	// Handle prefix commands in terminal mode
	if o.PrefixActive {
		return HandlePrefixCommand(msg, o)
	}

	// Direct terminal-mode binds and workspace switching, resolved through the
	// keybind registry so a rebind in config.toml takes effect. These must be
	// checked before the PTY forwarding below so their keys are not typed into
	// the shell.
	if handleTerminalModeBinds(msg, o) {
		return o, nil
	}

	// Handle Alt+Left/Right for scrolling tiling column navigation
	if o.AutoTiling && o.UseScrollingLayout {
		switch msg.String() {
		case "alt+left":
			o.ScrollingFocusLeft()
			return o, nil
		case "alt+right":
			o.ScrollingFocusRight()
			return o, nil
		}
	}

	keyStr := msg.String()

	// Command palette: ctrl+p (configurable, intercepted before terminal forwarding)
	if keyStr == "ctrl+p" {
		o.ShowCommandPalette = true
		o.CommandPaletteQuery = ""
		o.CommandPaletteSelected = 0
		o.CommandPaletteScroll = 0
		return o, nil
	}

	// Handle paste shortcuts - intercept and request clipboard via OSC 52.
	// Plain ctrl+v is deliberately excluded so it falls through to the passthrough
	// block and reaches the child PTY as 0x16 (needed for vim visual-block, etc.),
	// matching the tmux/zellij convention. Ctrl+Shift+V and host bracketed paste remain.
	if keyStr == "ctrl+shift+v" || keyStr == "super+v" || keyStr == "super+shift+v" {
		if focusedWindow != nil {
			// Use tea.ReadClipboard to request clipboard via OSC 52
			// This will generate a tea.ClipboardMsg which we handle in handler.go
			return o, tea.ReadClipboard
		}
		return o, nil
	}
	// Normal terminal mode - pass through all keys
	if focusedWindow != nil {
		appCursorKeys := false
		if focusedWindow.Terminal != nil {
			appCursorKeys = focusedWindow.Terminal.ApplicationCursorKeys()
		}

		// When kitty keyboard protocol is active, encode as CSI u
		var rawInput []byte
		if focusedWindow.Terminal != nil && focusedWindow.Terminal.KittyKeyboardFlags() != 0 {
			encoded := vt.EncodeKeyCSIu(vtKeyFromBubbletea(msg), focusedWindow.Terminal.KittyKeyboardFlags())
			if len(encoded) > 0 {
				rawInput = []byte(encoded)
			}
		}
		// Fall back to legacy encoding
		if len(rawInput) == 0 {
			rawInput = getRawKeyBytesWithMode(msg, appCursorKeys)
		}

		if len(rawInput) > 0 {
			// Record the keystroke for tape capture here, at the point where it
			// is actually forwarded to the PTY. Recording earlier (before prefix,
			// overlay, and copy-mode routing) captured keys that never reach the
			// shell, so tapes replayed prefix chords and stray characters.
			recordTerminalKey(o, msg)
			if err := focusedWindow.SendInput(rawInput); err != nil {
				// Terminal unavailable, switch back to window mode
				o.Mode = app.WindowManagementMode
				focusedWindow.InvalidateCache()
			}
			// Forward keystrokes to all multifocused windows.
			// MultifocusSet is keyed by window ID; iterate in slice order so
			// the send order stays stable across swaps and state sync.
			if len(o.MultifocusSet) > 0 {
				for idx, w := range o.Windows {
					if idx != o.FocusedWindow && o.MultifocusSet[w.ID] {
						_ = w.SendInput(rawInput)
					}
				}
			}
		}
	} else {
		// No focused window, switch back to window mode
		o.Mode = app.WindowManagementMode
	}
	return o, nil
}

// recordTerminalKey records a keystroke that is being forwarded to the focused
// window's PTY into the active tape recording. Printable single-byte ASCII is
// accumulated as a Type command; everything else is recorded as a KeyCombo.
// Workspace switches, mode switches, and overlay/prefix keys return before the
// PTY-forward path, so they are never recorded here (workspace switches are
// captured separately by SwitchToWorkspace).
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

// handleTerminalLayoutPrefix handles layout prefix commands (leader, L, ...).
// The layout sub-prefix has no config section of its own yet, so its two keys
// stay literal; entering it (prefix_layout) is configurable.
func handleTerminalLayoutPrefix(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.LayoutPrefixActive = false
	o.PrefixActive = false
	switch msg.String() {
	case "l":
		// Load layout
		templates, _ := app.LoadLayoutTemplates()
		o.ShowLayoutPicker = true
		o.LayoutPickerMode = "load"
		o.LayoutPickerItems = templates
		o.LayoutPickerQuery = ""
		o.LayoutPickerSelected = 0
		o.LayoutPickerScroll = 0
		return o, nil
	case "s":
		// Save layout
		o.ShowLayoutPicker = true
		o.LayoutPickerMode = "save"
		o.LayoutPickerQuery = ""
		o.LayoutPickerSelected = 0
		o.LayoutPickerScroll = 0
		return o, nil
	case "esc":
		return o, nil
	default:
		return o, nil
	}
}
