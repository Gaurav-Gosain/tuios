package tape

// TokenType represents the type of a token in a .tape file
type TokenType string

const (
	// Special tokens
	TOKEN_EOF     TokenType = "EOF"
	TOKEN_ILLEGAL TokenType = "ILLEGAL"
	TOKEN_COMMENT TokenType = "COMMENT"
	TOKEN_NEWLINE TokenType = "NEWLINE"

	// Literals
	TOKEN_STRING     TokenType = "STRING"
	TOKEN_NUMBER     TokenType = "NUMBER"
	TOKEN_DURATION   TokenType = "DURATION"
	TOKEN_IDENTIFIER TokenType = "IDENTIFIER"

	// Symbols
	TOKEN_PLUS   TokenType = "PLUS"
	TOKEN_AT     TokenType = "AT"
	TOKEN_COMMA  TokenType = "COMMA"
	TOKEN_SLASH  TokenType = "SLASH"
	TOKEN_LPAREN TokenType = "LPAREN"
	TOKEN_RPAREN TokenType = "RPAREN"

	// Commands - Basic
	TOKEN_TYPE      TokenType = "Type"
	TOKEN_SLEEP     TokenType = "Sleep"
	TOKEN_ENTER     TokenType = "Enter"
	TOKEN_SPACE     TokenType = "Space"
	TOKEN_BACKSPACE TokenType = "Backspace"
	TOKEN_DELETE    TokenType = "Delete"
	TOKEN_TAB       TokenType = "Tab"
	TOKEN_ESCAPE    TokenType = "Escape"

	// Commands - Navigation
	TOKEN_UP    TokenType = "Up"
	TOKEN_DOWN  TokenType = "Down"
	TOKEN_LEFT  TokenType = "Left"
	TOKEN_RIGHT TokenType = "Right"
	TOKEN_HOME  TokenType = "Home"
	TOKEN_END   TokenType = "End"

	// Commands - Modifiers
	TOKEN_CTRL  TokenType = "Ctrl"
	TOKEN_ALT   TokenType = "Alt"
	TOKEN_SHIFT TokenType = "Shift"

	// Commands - Mode Switching
	TOKEN_TERMINAL_MODE          TokenType = "TerminalMode"
	TOKEN_WINDOW_MANAGEMENT_MODE TokenType = "WindowManagementMode"

	// Commands - Window Management
	TOKEN_NEW_WINDOW      TokenType = "NewWindow"
	TOKEN_CLOSE_WINDOW    TokenType = "CloseWindow"
	TOKEN_NEXT_WINDOW     TokenType = "NextWindow"
	TOKEN_PREV_WINDOW     TokenType = "PrevWindow"
	TOKEN_FOCUS_WINDOW    TokenType = "FocusWindow"
	TOKEN_RENAME_WINDOW   TokenType = "RenameWindow"
	TOKEN_MINIMIZE_WINDOW TokenType = "MinimizeWindow"
	TOKEN_RESTORE_WINDOW  TokenType = "RestoreWindow"

	// Commands - Tiling
	TOKEN_TOGGLE_TILING   TokenType = "ToggleTiling"
	TOKEN_ENABLE_TILING   TokenType = "EnableTiling"
	TOKEN_DISABLE_TILING  TokenType = "DisableTiling"
	TOKEN_SNAP_LEFT       TokenType = "SnapLeft"
	TOKEN_SNAP_RIGHT      TokenType = "SnapRight"
	TOKEN_SNAP_FULLSCREEN TokenType = "SnapFullscreen"

	// Commands - Workspace
	TOKEN_SWITCH_WS          TokenType = "SwitchWorkspace"
	TOKEN_MOVE_TO_WS         TokenType = "MoveToWorkspace"
	TOKEN_MOVE_AND_FOLLOW_WS TokenType = "MoveAndFollowWorkspace"

	// Commands - Other Actions
	TOKEN_SPLIT TokenType = "Split"
	TOKEN_FOCUS TokenType = "Focus"

	// Commands - Synchronization
	TOKEN_WAIT             TokenType = "Wait"
	TOKEN_WAIT_UNTIL_REGEX TokenType = "WaitUntilRegex"

	// Commands - Settings (for future use)
	TOKEN_SET    TokenType = "Set"
	TOKEN_OUTPUT TokenType = "Output"
	TOKEN_SOURCE TokenType = "Source"

	// Keywords
	TOKEN_TRUE  TokenType = "true"
	TOKEN_FALSE TokenType = "false"
)

// Token represents a lexical token
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Column  int
}

// IsCommand returns true if the token type is a command
func (tt TokenType) IsCommand() bool {
	switch tt {
	case TOKEN_TYPE, TOKEN_SLEEP, TOKEN_ENTER, TOKEN_SPACE, TOKEN_BACKSPACE,
		TOKEN_DELETE, TOKEN_TAB, TOKEN_ESCAPE,
		TOKEN_UP, TOKEN_DOWN, TOKEN_LEFT, TOKEN_RIGHT, TOKEN_HOME, TOKEN_END,
		TOKEN_CTRL, TOKEN_ALT, TOKEN_SHIFT,
		TOKEN_TERMINAL_MODE, TOKEN_WINDOW_MANAGEMENT_MODE,
		TOKEN_NEW_WINDOW, TOKEN_CLOSE_WINDOW, TOKEN_NEXT_WINDOW, TOKEN_PREV_WINDOW,
		TOKEN_FOCUS_WINDOW, TOKEN_RENAME_WINDOW, TOKEN_MINIMIZE_WINDOW, TOKEN_RESTORE_WINDOW,
		TOKEN_TOGGLE_TILING, TOKEN_ENABLE_TILING, TOKEN_DISABLE_TILING,
		TOKEN_SNAP_LEFT, TOKEN_SNAP_RIGHT, TOKEN_SNAP_FULLSCREEN,
		TOKEN_SWITCH_WS, TOKEN_MOVE_TO_WS, TOKEN_MOVE_AND_FOLLOW_WS,
		TOKEN_SPLIT, TOKEN_FOCUS,
		TOKEN_WAIT, TOKEN_WAIT_UNTIL_REGEX,
		TOKEN_SET, TOKEN_OUTPUT, TOKEN_SOURCE:
		return true
	}
	return false
}

// IsModifier returns true if the token is a modifier key
func (tt TokenType) IsModifier() bool {
	switch tt {
	case TOKEN_CTRL, TOKEN_ALT, TOKEN_SHIFT:
		return true
	}
	return false
}

// IsNavigationKey returns true if the token is a navigation key
func (tt TokenType) IsNavigationKey() bool {
	switch tt {
	case TOKEN_UP, TOKEN_DOWN, TOKEN_LEFT, TOKEN_RIGHT, TOKEN_HOME, TOKEN_END:
		return true
	}
	return false
}

// KeywordTokenMap maps string keywords to token types
var KeywordTokenMap = map[string]TokenType{
	// Basic commands
	"Type":      TOKEN_TYPE,
	"Sleep":     TOKEN_SLEEP,
	"Enter":     TOKEN_ENTER,
	"Space":     TOKEN_SPACE,
	"Backspace": TOKEN_BACKSPACE,
	"Delete":    TOKEN_DELETE,
	"Tab":       TOKEN_TAB,
	"Escape":    TOKEN_ESCAPE,

	// Navigation
	"Up":    TOKEN_UP,
	"Down":  TOKEN_DOWN,
	"Left":  TOKEN_LEFT,
	"Right": TOKEN_RIGHT,
	"Home":  TOKEN_HOME,
	"End":   TOKEN_END,

	// Modifiers
	"Ctrl":  TOKEN_CTRL,
	"Alt":   TOKEN_ALT,
	"Shift": TOKEN_SHIFT,

	// Mode switching
	"TerminalMode":         TOKEN_TERMINAL_MODE,
	"WindowManagementMode": TOKEN_WINDOW_MANAGEMENT_MODE,

	// Window management
	"NewWindow":      TOKEN_NEW_WINDOW,
	"CloseWindow":    TOKEN_CLOSE_WINDOW,
	"NextWindow":     TOKEN_NEXT_WINDOW,
	"PrevWindow":     TOKEN_PREV_WINDOW,
	"FocusWindow":    TOKEN_FOCUS_WINDOW,
	"RenameWindow":   TOKEN_RENAME_WINDOW,
	"MinimizeWindow": TOKEN_MINIMIZE_WINDOW,
	"RestoreWindow":  TOKEN_RESTORE_WINDOW,

	// Tiling
	"ToggleTiling":   TOKEN_TOGGLE_TILING,
	"EnableTiling":   TOKEN_ENABLE_TILING,
	"DisableTiling":  TOKEN_DISABLE_TILING,
	"SnapLeft":       TOKEN_SNAP_LEFT,
	"SnapRight":      TOKEN_SNAP_RIGHT,
	"SnapFullscreen": TOKEN_SNAP_FULLSCREEN,

	// Workspace
	"SwitchWorkspace":        TOKEN_SWITCH_WS,
	"MoveToWorkspace":        TOKEN_MOVE_TO_WS,
	"MoveAndFollowWorkspace": TOKEN_MOVE_AND_FOLLOW_WS,

	// Other actions
	"Split": TOKEN_SPLIT,
	"Focus": TOKEN_FOCUS,

	// Synchronization
	"Wait":           TOKEN_WAIT,
	"WaitUntilRegex": TOKEN_WAIT_UNTIL_REGEX,

	// Settings
	"Set":    TOKEN_SET,
	"Output": TOKEN_OUTPUT,
	"Source": TOKEN_SOURCE,

	// Literals
	"true":  TOKEN_TRUE,
	"false": TOKEN_FALSE,
}

// LookupKeyword returns the token type for a keyword, or TOKEN_IDENTIFIER if not a keyword
func LookupKeyword(ident string) TokenType {
	if tt, ok := KeywordTokenMap[ident]; ok {
		return tt
	}
	return TOKEN_IDENTIFIER
}
