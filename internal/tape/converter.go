package tape

// ScriptMessageConverter handles conversion of script commands to internal messages
type ScriptMessageConverter struct{}

// NewScriptMessageConverter creates a new converter
func NewScriptMessageConverter() *ScriptMessageConverter {
	return &ScriptMessageConverter{}
}

// ConvertTypeCommand converts a Type command to a sequence of key strings
// Each character in the string is sent as a separate message
func (c *ScriptMessageConverter) ConvertTypeCommand(text string) []string {
	var messages []string
	for _, ch := range text {
		messages = append(messages, string(ch))
	}
	return messages
}

// ConvertKeyCommand converts a single key press to a key string
func (c *ScriptMessageConverter) ConvertKeyCommand(key string) string {
	return c.mapKeyName(key)
}

// ConvertKeyCombinationCommand converts a key combination (Ctrl+B, Alt+1) to a key string
func (c *ScriptMessageConverter) ConvertKeyCombinationCommand(combo string) string {
	// Parse the combo string: "Ctrl+B", "Alt+1", "Shift+Tab", etc.
	keyCombo, err := ParseKeyCombo(combo)
	if err != nil {
		// Fallback: treat as regular key
		return combo
	}

	return c.buildKeyString(keyCombo)
}

// buildKeyString builds a key string from a parsed KeyCombo
func (c *ScriptMessageConverter) buildKeyString(kc *KeyCombo) string {
	var result string

	// Handle Ctrl modifier
	if kc.Ctrl {
		result += "ctrl+"
	}

	// Handle Alt modifier
	if kc.Alt {
		result += "alt+"
	}

	// Handle Shift modifier
	if kc.Shift {
		result += "shift+"
	}

	// Add the key
	keyStr := c.mapKeyName(kc.Key)
	result += keyStr

	return result
}

// mapKeyName maps script key names to standardized key names
func (c *ScriptMessageConverter) mapKeyName(key string) string {
	keyMap := map[string]string{
		"Enter":     "enter",
		"enter":     "enter",
		"Return":    "enter",
		"Space":     " ",
		"space":     " ",
		"Tab":       "tab",
		"tab":       "tab",
		"Escape":    "esc",
		"esc":       "esc",
		"Backspace": "backspace",
		"backspace": "backspace",
		"Delete":    "delete",
		"delete":    "delete",
		"Up":        "up",
		"up":        "up",
		"Down":      "down",
		"down":      "down",
		"Left":      "left",
		"left":      "left",
		"Right":     "right",
		"right":     "right",
		"Home":      "home",
		"home":      "home",
		"End":       "end",
		"end":       "end",
		"PageUp":    "pgup",
		"pageup":    "pgup",
		"PageDown":  "pgdn",
		"pagedown":  "pgdn",
	}

	if mapped, ok := keyMap[key]; ok {
		return mapped
	}

	// Default: return lowercase version of key
	return key
}

// ActionMessage is a custom message for dispatching tuios actions
type ActionMessage struct {
	Action string
	Args   []string
}

// SleepMessage represents a sleep/delay command
type SleepMessage struct {
	Duration string // e.g., "500ms", "1s"
}

// ConvertCommandToKeyString converts a Command to a key string for input simulation
// Returns the key string and delay in milliseconds
func (c *ScriptMessageConverter) ConvertCommandToKeyString(cmd *Command) (keyStr string, delayMs int) {
	switch cmd.Type {
	case CommandTypeEnter, CommandTypeSpace, CommandTypeBackspace,
		CommandTypeDelete, CommandTypeTab, CommandTypeEscape,
		CommandTypeUp, CommandTypeDown, CommandTypeLeft, CommandTypeRight,
		CommandTypeHome, CommandTypeEnd:
		keyStr = c.ConvertKeyCommand(c.cmdTypeToKeyName(cmd.Type))
		if cmd.Delay > 0 {
			delayMs = int(cmd.Delay.Milliseconds())
		}

	case CommandTypeKeyCombo:
		if len(cmd.Args) > 0 {
			keyStr = c.ConvertKeyCombinationCommand(cmd.Args[0])
		}
		if cmd.Delay > 0 {
			delayMs = int(cmd.Delay.Milliseconds())
		}
	}

	return keyStr, delayMs
}

// cmdTypeToKeyName converts a CommandType to its key name
func (c *ScriptMessageConverter) cmdTypeToKeyName(cmdType CommandType) string {
	switch cmdType {
	case CommandTypeEnter:
		return "enter"
	case CommandTypeSpace:
		return "space"
	case CommandTypeBackspace:
		return "backspace"
	case CommandTypeDelete:
		return "delete"
	case CommandTypeTab:
		return "tab"
	case CommandTypeEscape:
		return "esc"
	case CommandTypeUp:
		return "up"
	case CommandTypeDown:
		return "down"
	case CommandTypeLeft:
		return "left"
	case CommandTypeRight:
		return "right"
	case CommandTypeHome:
		return "home"
	case CommandTypeEnd:
		return "end"
	default:
		return ""
	}
}
