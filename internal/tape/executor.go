package tape

import (
	"fmt"
	"strings"
	"unicode"
)

// Executor executes tape commands by directly manipulating the app state
// This bridges the gap between tape commands and tuios functionality
type Executor interface {
	// ExecuteCommand executes a single tape command
	ExecuteCommand(cmd *Command) error

	// GetFocusedWindowID returns the ID of the focused window
	GetFocusedWindowID() string

	// SendToWindow sends bytes to a window's PTY
	SendToWindow(windowID string, data []byte) error

	// Mode switching
	SetMode(mode string) error // "terminal" or "window"

	// Window management
	CreateNewWindow() error
	CreateNewWindowWithName(name string) error
	CloseWindow(windowID string) error
	CloseWindowByName(name string) error // Closes all windows with matching name
	NextWindow() error
	PrevWindow() error
	FocusWindowByID(windowID string) error
	FocusWindowByName(name string) error // Errors if multiple matches
	RenameWindowByID(windowID, name string) error
	RenameWindowByName(oldName, newName string) error // Errors if multiple matches
	MinimizeWindowByID(windowID string) error
	MinimizeWindowByName(name string) error // Errors if multiple matches
	RestoreWindowByID(windowID string) error
	RestoreWindowByName(name string) error // Errors if multiple matches

	// Tiling
	ToggleTiling() error
	EnableTiling() error
	DisableTiling() error
	SnapByDirection(direction string) error // "left", "right", "fullscreen"

	// BSP Tiling
	SplitHorizontal() error
	SplitVertical() error
	RotateSplit() error
	EqualizeSplitsExec() error
	Preselect(direction string) error // "left", "right", "up", "down"

	// Workspace
	SwitchWorkspace(workspace int) error
	MoveWindowToWorkspaceByID(windowID string, workspace int) error
	MoveAndFollowWorkspaceByID(windowID string, workspace int) error

	// Animations
	EnableAnimations() error
	DisableAnimations() error
	ToggleAnimations() error

	// Config commands for runtime configuration
	SetConfig(path, value string) error
	SetTheme(themeName string) error
	SetDockbarPosition(position string) error
	SetBorderStyle(style string) error
	ShowNotificationCmd(message, notificationType string) error
	FocusDirection(direction string) error
}

// CommandExecutor provides a default implementation
type CommandExecutor struct {
	executor Executor
}

// NewCommandExecutor creates a new command executor
func NewCommandExecutor(executor Executor) *CommandExecutor {
	return &CommandExecutor{executor: executor}
}

// Execute executes a command
func (ce *CommandExecutor) Execute(cmd *Command) error {
	if ce.executor == nil {
		return nil
	}

	switch cmd.Type {
	case CommandTypeType:
		if len(cmd.Args) > 0 {
			return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte(cmd.Args[0]))
		}

    case CommandTypeEnter:
        // Windows requires \r\n, Unix accepts \n
        if runtime.GOOS == "windows" {
            return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte{'\r', '\n'})
        }
        return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte{'\n'})

	case CommandTypeSpace:
		return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte{' '})

	case CommandTypeBackspace:
		return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte{'\b'})

	case CommandTypeTab:
		return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte{'\t'})

	case CommandTypeEscape:
		return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), []byte{0x1b})

	// Mode switching
	case CommandTypeTerminalMode:
		return ce.executor.SetMode("terminal")

	case CommandTypeWindowManagementMode:
		return ce.executor.SetMode("window")

	// Window management
	case CommandTypeNewWindow:
		if len(cmd.Args) > 0 && cmd.Args[0] != "" {
			return ce.executor.CreateNewWindowWithName(cmd.Args[0])
		}
		return ce.executor.CreateNewWindow()

	case CommandTypeCloseWindow:
		if len(cmd.Args) > 0 && cmd.Args[0] != "" {
			return ce.executor.CloseWindowByName(cmd.Args[0])
		}
		return ce.executor.CloseWindow(ce.executor.GetFocusedWindowID())

	case CommandTypeNextWindow:
		return ce.executor.NextWindow()

	case CommandTypePrevWindow:
		return ce.executor.PrevWindow()

	case CommandTypeFocusWindow:
		if len(cmd.Args) > 0 && cmd.Args[0] != "" {
			// Try as name first (more user-friendly), fall back to ID
			if err := ce.executor.FocusWindowByName(cmd.Args[0]); err != nil {
				// If name lookup fails, try as ID
				return ce.executor.FocusWindowByID(cmd.Args[0])
			}
			return nil
		}

	case CommandTypeRenameWindow:
		if len(cmd.Args) >= 2 {
			// Two args: old name, new name
			return ce.executor.RenameWindowByName(cmd.Args[0], cmd.Args[1])
		} else if len(cmd.Args) == 1 {
			// One arg: rename focused window
			return ce.executor.RenameWindowByID(ce.executor.GetFocusedWindowID(), cmd.Args[0])
		}

	case CommandTypeMinimizeWindow:
		if len(cmd.Args) > 0 && cmd.Args[0] != "" {
			return ce.executor.MinimizeWindowByName(cmd.Args[0])
		}
		return ce.executor.MinimizeWindowByID(ce.executor.GetFocusedWindowID())

	case CommandTypeRestoreWindow:
		if len(cmd.Args) > 0 && cmd.Args[0] != "" {
			return ce.executor.RestoreWindowByName(cmd.Args[0])
		}
		return ce.executor.RestoreWindowByID(ce.executor.GetFocusedWindowID())

	// Tiling
	case CommandTypeToggleTiling:
		return ce.executor.ToggleTiling()

	case CommandTypeEnableTiling:
		return ce.executor.EnableTiling()

	case CommandTypeDisableTiling:
		return ce.executor.DisableTiling()

	case CommandTypeSnapLeft:
		return ce.executor.SnapByDirection("left")

	case CommandTypeSnapRight:
		return ce.executor.SnapByDirection("right")

	case CommandTypeSnapFullscreen:
		return ce.executor.SnapByDirection("fullscreen")

	// BSP Tiling
	case CommandTypeSplit:
		if len(cmd.Args) > 0 {
			direction := strings.ToLower(cmd.Args[0])
			switch direction {
			case "horizontal", "h":
				return ce.executor.SplitHorizontal()
			case "vertical", "v":
				return ce.executor.SplitVertical()
			}
		}
		return nil

	case CommandTypeRotateSplit:
		return ce.executor.RotateSplit()

	case CommandTypeEqualizeSplits:
		return ce.executor.EqualizeSplitsExec()

	case CommandTypePreselect:
		if len(cmd.Args) > 0 {
			return ce.executor.Preselect(strings.ToLower(cmd.Args[0]))
		}
		return nil

	// Workspace
	case CommandTypeSwitchWS:
		if len(cmd.Args) > 0 {
			ws := 0
			_, _ = fmt.Sscanf(cmd.Args[0], "%d", &ws)
			return ce.executor.SwitchWorkspace(ws)
		}

	case CommandTypeMoveToWS:
		if len(cmd.Args) > 0 {
			ws := 0
			_, _ = fmt.Sscanf(cmd.Args[0], "%d", &ws)
			return ce.executor.MoveWindowToWorkspaceByID(ce.executor.GetFocusedWindowID(), ws)
		}

	case CommandTypeMoveAndFollowWS:
		if len(cmd.Args) > 0 {
			ws := 0
			_, _ = fmt.Sscanf(cmd.Args[0], "%d", &ws)
			return ce.executor.MoveAndFollowWorkspaceByID(ce.executor.GetFocusedWindowID(), ws)
		}

	case CommandTypeKeyCombo:
		if len(cmd.Args) > 0 {
			comboStr := cmd.Args[0]
			// Handle Alt+N / alt+N for workspace switching (case-insensitive)
			lowerCombo := strings.ToLower(comboStr)
			if len(lowerCombo) >= 5 && (lowerCombo[:4] == "alt+" || lowerCombo[:4] == "opt+") {
				wsStr := comboStr[4:]
				ws := 0
				if _, err := fmt.Sscanf(wsStr, "%d", &ws); err == nil && ws >= 1 && ws <= 9 {
					return ce.executor.SwitchWorkspace(ws)
				}
			}
			// For other key combos, convert to proper bytes and send to the focused window
			keyBytes := convertKeyComboToBytes(comboStr)
			return ce.executor.SendToWindow(ce.executor.GetFocusedWindowID(), keyBytes)
		}

	case CommandTypeWaitUntilRegex:
		// WaitUntilRegex requires special handling - it needs to be processed in a different way
		// by the script playback system since it requires checking PTY output
		// For now, we return a special error to signal this to the playback system
		// The actual implementation will be handled in the playback loop
		return nil

	case CommandTypeEnableAnimations:
		return ce.executor.EnableAnimations()

	case CommandTypeDisableAnimations:
		return ce.executor.DisableAnimations()

	case CommandTypeToggleAnimations:
		return ce.executor.ToggleAnimations()

	// Config commands
	case CommandTypeSetConfig:
		if len(cmd.Args) >= 2 {
			return ce.executor.SetConfig(cmd.Args[0], cmd.Args[1])
		}
		return nil

	case CommandTypeSetTheme:
		if len(cmd.Args) > 0 {
			return ce.executor.SetTheme(cmd.Args[0])
		}
		return nil

	case CommandTypeSetDockbarPosition:
		if len(cmd.Args) > 0 {
			return ce.executor.SetDockbarPosition(cmd.Args[0])
		}
		return nil

	case CommandTypeSetBorderStyle:
		if len(cmd.Args) > 0 {
			return ce.executor.SetBorderStyle(cmd.Args[0])
		}
		return nil

	case CommandTypeShowNotification:
		if len(cmd.Args) > 0 {
			notifType := "info"
			if len(cmd.Args) > 1 {
				notifType = cmd.Args[1]
			}
			return ce.executor.ShowNotificationCmd(cmd.Args[0], notifType)
		}
		return nil

	case CommandTypeFocusDirection:
		if len(cmd.Args) > 0 {
			return ce.executor.FocusDirection(strings.ToLower(cmd.Args[0]))
		}
		return nil

	// Other command types are handled elsewhere or ignored
	default:
		return nil
	}

	return nil
}

// convertKeyComboToBytes converts a key combination string to actual bytes to send to the PTY
// Examples: "Ctrl+b" -> [0x02], "Alt+x" -> [0x1b, 'x']
func convertKeyComboToBytes(comboStr string) []byte {
	parts := strings.Split(comboStr, "+")
	if len(parts) < 2 {
		return []byte(comboStr)
	}

	var result []byte
	var ctrlModifier, altModifier bool
	var keyStr string

	// Parse modifiers and key
	for i := range len(parts) {
		part := strings.TrimSpace(parts[i])
		switch strings.ToLower(part) {
		case "ctrl":
			ctrlModifier = true
		case "alt", "opt":
			altModifier = true
		case "shift":
			// Shift is harder to handle without more context, just ignore for now
		default:
			keyStr = part
		}
	}

	if keyStr == "" {
		return []byte(comboStr)
	}

	// Convert the key string to bytes
	if len(keyStr) == 1 {
		keyChar := keyStr[0]

		// Apply Ctrl modifier: produces ASCII control characters (0x00-0x1F)
		if ctrlModifier {
			// Ctrl+letter: subtract 64 from uppercase, or use bitwise AND with 0x1F
			if unicode.IsLetter(rune(keyChar)) {
				// Convert to uppercase equivalent and apply Ctrl
				upperChar := byte(unicode.ToUpper(rune(keyChar)))
				result = append(result, upperChar&0x1F)
			} else if unicode.IsDigit(rune(keyChar)) {
				// Ctrl+digit
				result = append(result, keyChar&0x1F)
			}
		} else if altModifier {
			// Alt modifier: send ESC followed by the character
			result = append(result, 0x1b) // ESC
			result = append(result, keyChar)
		} else {
			result = append(result, keyChar)
		}
	} else {
		// Multi-character key (like "space", "enter", etc.)
		// Map special keys
		specialKeys := map[string][]byte{
			"space":     {' '},
			"enter":     {'\n'},
			"return":    {'\n'},
			"tab":       {'\t'},
			"escape":    {0x1b},
			"esc":       {0x1b},
			"backspace": {'\b'},
			"delete":    {0x7f},
		}

		if keyBytes, exists := specialKeys[strings.ToLower(keyStr)]; exists {
			if altModifier {
				result = append(result, 0x1b) // ESC prefix for Alt
			}
			result = append(result, keyBytes...)
		} else {
			// Unknown key, just send it as-is
			result = append(result, []byte(keyStr)...)
		}
	}

	return result
}
