package tape

import (
	"fmt"
	"time"
)

// CommandType represents the type of a tape command
type CommandType string

const (
	// Basic commands
	CommandType_Type      CommandType = "Type"
	CommandType_Sleep     CommandType = "Sleep"
	CommandType_Enter     CommandType = "Enter"
	CommandType_Space     CommandType = "Space"
	CommandType_Backspace CommandType = "Backspace"
	CommandType_Delete    CommandType = "Delete"
	CommandType_Tab       CommandType = "Tab"
	CommandType_Escape    CommandType = "Escape"

	// Navigation keys
	CommandType_Up    CommandType = "Up"
	CommandType_Down  CommandType = "Down"
	CommandType_Left  CommandType = "Left"
	CommandType_Right CommandType = "Right"
	CommandType_Home  CommandType = "Home"
	CommandType_End   CommandType = "End"

	// Key combinations (Ctrl+X, Alt+X, etc.)
	CommandType_KeyCombo CommandType = "KeyCombo"

	// Mode switching
	CommandType_TerminalMode          CommandType = "TerminalMode"
	CommandType_WindowManagementMode  CommandType = "WindowManagementMode"

	// Window management
	CommandType_NewWindow     CommandType = "NewWindow"
	CommandType_CloseWindow   CommandType = "CloseWindow"
	CommandType_NextWindow    CommandType = "NextWindow"
	CommandType_PrevWindow    CommandType = "PrevWindow"
	CommandType_FocusWindow   CommandType = "FocusWindow"
	CommandType_RenameWindow  CommandType = "RenameWindow"
	CommandType_MinimizeWindow CommandType = "MinimizeWindow"
	CommandType_RestoreWindow CommandType = "RestoreWindow"

	// Tiling
	CommandType_ToggleTiling CommandType = "ToggleTiling"
	CommandType_EnableTiling CommandType = "EnableTiling"
	CommandType_DisableTiling CommandType = "DisableTiling"
	CommandType_SnapLeft      CommandType = "SnapLeft"
	CommandType_SnapRight     CommandType = "SnapRight"
	CommandType_SnapFullscreen CommandType = "SnapFullscreen"

	// Workspace
	CommandType_SwitchWS          CommandType = "SwitchWorkspace"
	CommandType_MoveToWS          CommandType = "MoveToWorkspace"
	CommandType_MoveAndFollowWS   CommandType = "MoveAndFollowWorkspace"

	// Other actions
	CommandType_Split CommandType = "Split"
	CommandType_Focus CommandType = "Focus"

	// Synchronization
	CommandType_Wait            CommandType = "Wait"
	CommandType_WaitUntilRegex  CommandType = "WaitUntilRegex"

	// Settings
	CommandType_Set    CommandType = "Set"
	CommandType_Output CommandType = "Output"
	CommandType_Source CommandType = "Source"

	// Comment
	CommandType_Comment CommandType = "Comment"
)

// Command represents a parsed tape command
type Command struct {
	Type       CommandType
	Args       []string      // Command arguments
	Delay      time.Duration // Delay after this command
	Line       int           // Source line number
	Column     int           // Source column number
	Raw        string        // Original raw command text
}

// String returns a string representation of the command
func (c *Command) String() string {
	switch c.Type {
	case CommandType_Type:
		return fmt.Sprintf("Type %q", c.Args)
	case CommandType_Sleep:
		return fmt.Sprintf("Sleep %v", c.Args)
	case CommandType_KeyCombo:
		return fmt.Sprintf("%s", c.Args)
	case CommandType_SwitchWS:
		return fmt.Sprintf("SwitchWorkspace %s", c.Args)
	default:
		return fmt.Sprintf("%s %v", c.Type, c.Args)
	}
}

// IsCommand returns true if the command type is a valid command
func (ct CommandType) IsCommand() bool {
	switch ct {
	case CommandType_Type, CommandType_Sleep, CommandType_Enter, CommandType_Space,
		CommandType_Backspace, CommandType_Delete, CommandType_Tab, CommandType_Escape,
		CommandType_Up, CommandType_Down, CommandType_Left, CommandType_Right,
		CommandType_Home, CommandType_End, CommandType_KeyCombo,
		CommandType_TerminalMode, CommandType_WindowManagementMode,
		CommandType_NewWindow, CommandType_CloseWindow, CommandType_NextWindow,
		CommandType_PrevWindow, CommandType_FocusWindow, CommandType_RenameWindow,
		CommandType_MinimizeWindow, CommandType_RestoreWindow,
		CommandType_ToggleTiling, CommandType_EnableTiling, CommandType_DisableTiling,
		CommandType_SnapLeft, CommandType_SnapRight, CommandType_SnapFullscreen,
		CommandType_SwitchWS, CommandType_MoveToWS, CommandType_MoveAndFollowWS,
		CommandType_Split, CommandType_Focus,
		CommandType_Wait, CommandType_WaitUntilRegex,
		CommandType_Set, CommandType_Output, CommandType_Source,
		CommandType_Comment:
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

	// All parts before the last are modifiers
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "Ctrl":
			kc.Ctrl = true
		case "Alt":
			kc.Alt = true
		case "Shift":
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
	for i := 0; i < len(s); i++ {
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
