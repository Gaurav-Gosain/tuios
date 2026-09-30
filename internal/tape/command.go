// Package tape provides functionality for parsing, storing, and executing automation scripts in .tape format.
package tape

import (
	"fmt"
	"strings"
	"time"
	"unicode"
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
	// CommandTypeArrangePanes lays the panes of the current workspace out as
	// tiled, even-horizontal or even-vertical.
	CommandTypeArrangePanes CommandType = "ArrangePanes"
	// CommandTypeSetMultifocus makes the multifocus set exactly the named
	// windows. With no windows it clears the set.
	CommandTypeSetMultifocus CommandType = "SetMultifocus"

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

	// CommandTypeToggleZoom represents the ToggleZoom command.
	CommandTypeToggleZoom CommandType = "ToggleZoom"
	// CommandTypeScreenshot represents the Screenshot command, which renders
	// the focused window to an image file so a capture is recordable and
	// replayable in a tape.
	CommandTypeScreenshot CommandType = "Screenshot"
	// CommandTypeSmartSplit represents the SmartSplit command.
	CommandTypeSmartSplit CommandType = "SmartSplit"
	// CommandTypeCommandPalette represents the CommandPalette command.
	CommandTypeCommandPalette CommandType = "CommandPalette"
	// CommandTypeSaveLayout represents the SaveLayout command.
	CommandTypeSaveLayout CommandType = "SaveLayout"
	// CommandTypeLoadLayout represents the LoadLayout command.
	CommandTypeLoadLayout CommandType = "LoadLayout"
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
		return strings.Join(c.Args, " ")
	case CommandTypeSwitchWS:
		return fmt.Sprintf("SwitchWorkspace %s", c.Args)
	default:
		return fmt.Sprintf("%s %v", c.Type, c.Args)
	}
}

// commandTypes is every command a tape or a remote caller can run. IsCommand
// and ResolveCommandName both read it, so a command added here is known to
// both at once.
var commandTypes = []CommandType{
	CommandTypeType,
	CommandTypeSleep,
	CommandTypeEnter,
	CommandTypeSpace,
	CommandTypeBackspace,
	CommandTypeDelete,
	CommandTypeTab,
	CommandTypeEscape,
	CommandTypeUp,
	CommandTypeDown,
	CommandTypeLeft,
	CommandTypeRight,
	CommandTypeHome,
	CommandTypeEnd,
	CommandTypeKeyCombo,
	CommandTypeTerminalMode,
	CommandTypeWindowManagementMode,
	CommandTypeNewWindow,
	CommandTypeCloseWindow,
	CommandTypeNextWindow,
	CommandTypePrevWindow,
	CommandTypeFocusWindow,
	CommandTypeRenameWindow,
	CommandTypeMinimizeWindow,
	CommandTypeRestoreWindow,
	CommandTypeToggleTiling,
	CommandTypeEnableTiling,
	CommandTypeDisableTiling,
	CommandTypeSnapLeft,
	CommandTypeSnapRight,
	CommandTypeSnapFullscreen,
	CommandTypeSwitchWS,
	CommandTypeMoveToWS,
	CommandTypeMoveAndFollowWS,
	CommandTypeSplit,
	CommandTypeFocus,
	CommandTypeRotateSplit,
	CommandTypeEqualizeSplits,
	CommandTypePreselect,
	CommandTypeArrangePanes,
	CommandTypeSetMultifocus,
	CommandTypeWait,
	CommandTypeWaitUntilRegex,
	CommandTypeSet,
	CommandTypeOutput,
	CommandTypeSource,
	CommandTypeEnableAnimations,
	CommandTypeDisableAnimations,
	CommandTypeToggleAnimations,
	CommandTypeComment,
	CommandTypeSetConfig,
	CommandTypeSetTheme,
	CommandTypeSetDockbarPosition,
	CommandTypeSetBorderStyle,
	CommandTypeShowNotification,
	CommandTypeFocusDirection,
	CommandTypeToggleZoom,
	CommandTypeScreenshot,
	CommandTypeSmartSplit,
	CommandTypeCommandPalette,
	CommandTypeSaveLayout,
	CommandTypeLoadLayout,
}

// commandTypeSet is commandTypes as a set, and commandTypesByKey is the same
// commands keyed by their normalized name (see commandKey), which is what lets
// a caller outside a tape name one the way the keymap does.
var (
	commandTypeSet    = make(map[CommandType]bool, len(commandTypes))
	commandTypesByKey = make(map[string]CommandType, len(commandTypes))
)

func init() {
	for _, ct := range commandTypes {
		commandTypeSet[ct] = true
		commandTypesByKey[commandKey(string(ct))] = ct
	}
}

// commandKey folds a command name for lookup: case and the separators a
// keymap name uses are dropped, so "ToggleTiling", "toggletiling" and
// "toggle_tiling" are one key.
func commandKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// IsCommand returns true if the command type is a valid command
func (ct CommandType) IsCommand() bool {
	return commandTypeSet[ct]
}

// ResolveCommandName returns the command a name from outside a tape refers
// to: the tape's own name ("ToggleTiling"), that name in any case, or the
// name the keymap gives the same action ("toggle_tiling"). run-command is the
// escape hatch for a binding with no verb, and a binding is known by its
// keymap name, so that spelling has to reach the same command. A name that is
// none of these is reported rather than run as nothing.
func ResolveCommandName(name string) (CommandType, bool) {
	if ct := CommandType(name); ct.IsCommand() {
		return ct, true
	}
	ct, ok := commandTypesByKey[commandKey(name)]
	return ct, ok
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
