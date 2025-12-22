// Package tape provides functionality for parsing, storing, and executing automation scripts in .tape format.
package tape

import (
	"fmt"
	"strings"
	"time"
)

// CommandType represents the type of a tape command
type CommandType string

const (
	// CommandTypeType represents the Type command for text input.
	CommandTypeType CommandType = "Type"
	// CommandTypeSleep represents the Sleep command for delays.
	CommandTypeSleep CommandType = "Sleep"
	// CommandTypeEnter represents the Enter key command.
	CommandTypeEnter CommandType = "Enter"
	// CommandTypeSpace represents the Space key command.
	CommandTypeSpace CommandType = "Space"
	// CommandTypeBackspace represents the Backspace key command.
	CommandTypeBackspace CommandType = "Backspace"
	// CommandTypeDelete represents the Delete key command.
	CommandTypeDelete CommandType = "Delete"
	// CommandTypeTab represents the Tab key command.
	CommandTypeTab CommandType = "Tab"
	// CommandTypeEscape represents the Escape key command.
	CommandTypeEscape CommandType = "Escape"

	// CommandTypeUp represents the Up navigation key command.
	CommandTypeUp CommandType = "Up"
	// CommandTypeDown represents the Down navigation key command.
	CommandTypeDown CommandType = "Down"
	// CommandTypeLeft represents the Left navigation key command.
	CommandTypeLeft CommandType = "Left"
	// CommandTypeRight represents the Right navigation key command.
	CommandTypeRight CommandType = "Right"
	// CommandTypeHome represents the Home navigation key command.
	CommandTypeHome CommandType = "Home"
	// CommandTypeEnd represents the End navigation key command.
	CommandTypeEnd CommandType = "End"

	// CommandTypeKeyCombo represents a key combination command (Ctrl+X, Alt+X, etc.).
	CommandTypeKeyCombo CommandType = "KeyCombo"

	// CommandTypeTerminalMode represents the TerminalMode command.
	CommandTypeTerminalMode CommandType = "TerminalMode"
	// CommandTypeWindowManagementMode represents the WindowManagementMode command.
	CommandTypeWindowManagementMode CommandType = "WindowManagementMode"

	// CommandTypeNewWindow represents the NewWindow command.
	CommandTypeNewWindow CommandType = "NewWindow"
	// CommandTypeCloseWindow represents the CloseWindow command.
	CommandTypeCloseWindow CommandType = "CloseWindow"
	// CommandTypeNextWindow represents the NextWindow command.
	CommandTypeNextWindow CommandType = "NextWindow"
	// CommandTypePrevWindow represents the PrevWindow command.
	CommandTypePrevWindow CommandType = "PrevWindow"
	// CommandTypeFocusWindow represents the FocusWindow command.
	CommandTypeFocusWindow CommandType = "FocusWindow"
	// CommandTypeRenameWindow represents the RenameWindow command.
	CommandTypeRenameWindow CommandType = "RenameWindow"
	// CommandTypeMinimizeWindow represents the MinimizeWindow command.
	CommandTypeMinimizeWindow CommandType = "MinimizeWindow"
	// CommandTypeRestoreWindow represents the RestoreWindow command.
	CommandTypeRestoreWindow CommandType = "RestoreWindow"

	// CommandTypeToggleTiling represents the ToggleTiling command.
	CommandTypeToggleTiling CommandType = "ToggleTiling"
	// CommandTypeEnableTiling represents the EnableTiling command.
	CommandTypeEnableTiling CommandType = "EnableTiling"
	// CommandTypeDisableTiling represents the DisableTiling command.
	CommandTypeDisableTiling CommandType = "DisableTiling"
	// CommandTypeSnapLeft represents the SnapLeft command.
	CommandTypeSnapLeft CommandType = "SnapLeft"
	// CommandTypeSnapRight represents the SnapRight command.
	CommandTypeSnapRight CommandType = "SnapRight"
	// CommandTypeSnapFullscreen represents the SnapFullscreen command.
	CommandTypeSnapFullscreen CommandType = "SnapFullscreen"

	// CommandTypeSwitchWS represents the SwitchWorkspace command.
	CommandTypeSwitchWS CommandType = "SwitchWorkspace"
	// CommandTypeMoveToWS represents the MoveToWorkspace command.
	CommandTypeMoveToWS CommandType = "MoveToWorkspace"
	// CommandTypeMoveAndFollowWS represents the MoveAndFollowWorkspace command.
	CommandTypeMoveAndFollowWS CommandType = "MoveAndFollowWorkspace"

	// CommandTypeSplit represents the Split command (horizontal/vertical).
	CommandTypeSplit CommandType = "Split"
	// CommandTypeFocus represents the Focus command.
	CommandTypeFocus CommandType = "Focus"
	// CommandTypeRotateSplit represents the RotateSplit command.
	CommandTypeRotateSplit CommandType = "RotateSplit"
	// CommandTypeEqualizeSplits represents the EqualizeSplits command.
	CommandTypeEqualizeSplits CommandType = "EqualizeSplits"
	// CommandTypePreselect represents the Preselect command.
	CommandTypePreselect CommandType = "Preselect"

	// CommandTypeWait represents the Wait command.
	CommandTypeWait CommandType = "Wait"
	// CommandTypeWaitUntilRegex represents the WaitUntilRegex command.
	CommandTypeWaitUntilRegex CommandType = "WaitUntilRegex"

	// CommandTypeSet represents the Set command.
	CommandTypeSet CommandType = "Set"
	// CommandTypeOutput represents the Output command.
	CommandTypeOutput CommandType = "Output"
	// CommandTypeSource represents the Source command.
	CommandTypeSource CommandType = "Source"

	// CommandTypeEnableAnimations represents the EnableAnimations command.
	CommandTypeEnableAnimations CommandType = "EnableAnimations"
	// CommandTypeDisableAnimations represents the DisableAnimations command.
	CommandTypeDisableAnimations CommandType = "DisableAnimations"
	// CommandTypeToggleAnimations represents the ToggleAnimations command.
	CommandTypeToggleAnimations CommandType = "ToggleAnimations"

	// CommandTypeComment represents a comment line.
	CommandTypeComment CommandType = "Comment"

	// Config commands for runtime configuration changes
	// CommandTypeSetConfig sets a configuration option at runtime.
	CommandTypeSetConfig CommandType = "SetConfig"
	// CommandTypeSetTheme changes the active theme.
	CommandTypeSetTheme CommandType = "SetTheme"
	// CommandTypeSetDockbarPosition changes the dockbar position.
	CommandTypeSetDockbarPosition CommandType = "SetDockbarPosition"
	// CommandTypeSetBorderStyle changes the window border style.
	CommandTypeSetBorderStyle CommandType = "SetBorderStyle"
	// CommandTypeShowNotification displays a notification.
	CommandTypeShowNotification CommandType = "ShowNotification"
	// CommandTypeFocusDirection focuses a window in a direction.
	CommandTypeFocusDirection CommandType = "FocusDirection"
)

// Command represents a parsed tape command
type Command struct {
	Type   CommandType
	Args   []string      // Command arguments
	Delay  time.Duration // Delay after this command
	Line   int           // Source line number
	Column int           // Source column number
	Raw    string        // Original raw command text
}

// String returns a string representation of the command
func (c *Command) String() string {
	switch c.Type {
	case CommandTypeType:
		return fmt.Sprintf("Type %q", c.Args)
	case CommandTypeSleep:
		return fmt.Sprintf("Sleep %v", c.Args)
	case CommandTypeKeyCombo:
		return fmt.Sprintf("%s", c.Args)
	case CommandTypeSwitchWS:
		return fmt.Sprintf("SwitchWorkspace %s", c.Args)
	default:
		return fmt.Sprintf("%s %v", c.Type, c.Args)
	}
}

// IsCommand returns true if the command type is a valid command
func (ct CommandType) IsCommand() bool {
	switch ct {
	case CommandTypeType, CommandTypeSleep, CommandTypeEnter, CommandTypeSpace,
		CommandTypeBackspace, CommandTypeDelete, CommandTypeTab, CommandTypeEscape,
		CommandTypeUp, CommandTypeDown, CommandTypeLeft, CommandTypeRight,
		CommandTypeHome, CommandTypeEnd, CommandTypeKeyCombo,
		CommandTypeTerminalMode, CommandTypeWindowManagementMode,
		CommandTypeNewWindow, CommandTypeCloseWindow, CommandTypeNextWindow,
		CommandTypePrevWindow, CommandTypeFocusWindow, CommandTypeRenameWindow,
		CommandTypeMinimizeWindow, CommandTypeRestoreWindow,
		CommandTypeToggleTiling, CommandTypeEnableTiling, CommandTypeDisableTiling,
		CommandTypeSnapLeft, CommandTypeSnapRight, CommandTypeSnapFullscreen,
		CommandTypeSwitchWS, CommandTypeMoveToWS, CommandTypeMoveAndFollowWS,
		CommandTypeSplit, CommandTypeFocus, CommandTypeRotateSplit,
		CommandTypeEqualizeSplits, CommandTypePreselect,
		CommandTypeWait, CommandTypeWaitUntilRegex,
		CommandTypeSet, CommandTypeOutput, CommandTypeSource,
		CommandTypeEnableAnimations, CommandTypeDisableAnimations, CommandTypeToggleAnimations,
		CommandTypeComment,
		// Config commands
		CommandTypeSetConfig, CommandTypeSetTheme, CommandTypeSetDockbarPosition,
		CommandTypeSetBorderStyle, CommandTypeShowNotification, CommandTypeFocusDirection:
		return true
	}
	return false
}

// ParseDuration parses a duration string (e.g., "500ms", "1s")
func ParseDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

// KeyCombo represents a key combination (e.g., Ctrl+B, Alt+1)
type KeyCombo struct {
	Ctrl  bool
	Alt   bool
	Shift bool
	Key   string // The key itself (b, 1, etc.)
}

// String returns a string representation of the key combo
func (kc *KeyCombo) String() string {
	var result string
	if kc.Ctrl {
		result += "Ctrl+"
	}
	if kc.Alt {
		result += "Alt+"
	}
	if kc.Shift {
		result += "Shift+"
	}
	result += kc.Key
	return result
}

// ParseKeyCombo parses a key combo string like "Ctrl+B" or "Alt+Shift+1"
func ParseKeyCombo(s string) (*KeyCombo, error) {
	kc := &KeyCombo{}
	parts := splitKeyComboParts(s)

	if len(parts) == 0 {
		return nil, fmt.Errorf("empty key combo")
	}

	// Last part is always the key
	kc.Key = parts[len(parts)-1]

	// All parts before the last are modifiers (case-insensitive)
	for i := range len(parts) - 1 {
		switch strings.ToLower(parts[i]) {
		case "ctrl":
			kc.Ctrl = true
		case "alt", "opt":
			kc.Alt = true
		case "shift":
			kc.Shift = true
		default:
			return nil, fmt.Errorf("unknown modifier: %s", parts[i])
		}
	}

	return kc, nil
}

// splitKeyComboParts splits "Ctrl+Alt+B" into ["Ctrl", "Alt", "B"]
func splitKeyComboParts(s string) []string {
	var parts []string
	var current string
	for i := range len(s) {
		if s[i] == '+' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(s[i])
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
