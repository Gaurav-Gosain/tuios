package tape

import "strings"

// TokenType represents the type of a token in a .tape file
type TokenType string

const (
	// TokenEOF represents end of file token.
	TokenEOF TokenType = "EOF"
	// TokenIllegal represents an illegal token.
	TokenIllegal TokenType = "ILLEGAL"
	// TokenComment represents a comment token.
	TokenComment TokenType = "COMMENT"
	// TokenNewline represents a newline token.
	TokenNewline TokenType = "NEWLINE"
	// TokenString represents a string literal token.
	TokenString TokenType = "STRING"
	// TokenNumber represents a numeric literal token.
	TokenNumber TokenType = "NUMBER"
	// TokenDuration represents a duration token.
	TokenDuration TokenType = "DURATION"
	// TokenIdentifier represents an identifier token.
	TokenIdentifier TokenType = "IDENTIFIER"
	// TokenPlus represents the plus operator token.
	TokenPlus TokenType = "PLUS"
	// TokenAt represents the at operator token.
	TokenAt TokenType = "AT"
	// TokenComma represents the comma token.
	TokenComma TokenType = "COMMA"
	// TokenSlash represents the slash operator token.
	TokenSlash TokenType = "SLASH"
	// TokenLParen represents the left parenthesis token.
	TokenLParen TokenType = "LPAREN"
	// TokenRParen represents the right parenthesis token.
	TokenRParen TokenType = "RPAREN"
	// TokenTypeCmd represents the Type command token.
	TokenTypeCmd TokenType = "Type"
	// TokenSleep represents the Sleep command token.
	TokenSleep TokenType = "Sleep"
	// TokenEnter represents the Enter key command token.
	TokenEnter TokenType = "Enter"
	// TokenSpace represents the Space key command token.
	TokenSpace TokenType = "Space"
	// TokenBackspace represents the Backspace key command token.
	TokenBackspace TokenType = "Backspace"
	// TokenDelete represents the Delete key command token.
	TokenDelete TokenType = "Delete"
	// TokenTab represents the Tab key command token.
	TokenTab TokenType = "Tab"
	// TokenEscape represents the Escape key command token.
	TokenEscape TokenType = "Escape"
	// TokenUp represents the Up navigation key token.
	TokenUp TokenType = "Up"
	// TokenDown represents the Down navigation key token.
	TokenDown TokenType = "Down"
	// TokenLeft represents the Left navigation key token.
	TokenLeft TokenType = "Left"
	// TokenRight represents the Right navigation key token.
	TokenRight TokenType = "Right"
	// TokenHome represents the Home navigation key token.
	TokenHome TokenType = "Home"
	// TokenEnd represents the End navigation key token.
	TokenEnd TokenType = "End"
	// TokenCtrl represents the Ctrl modifier token.
	TokenCtrl TokenType = "Ctrl"
	// TokenAlt represents the Alt modifier token.
	TokenAlt TokenType = "Alt"
	// TokenShift represents the Shift modifier token.
	TokenShift TokenType = "Shift"
	// TokenTerminalMode represents the TerminalMode command token.
	TokenTerminalMode TokenType = "TerminalMode"
	// TokenWindowManagementMode represents the WindowManagementMode command token.
	TokenWindowManagementMode TokenType = "WindowManagementMode"
	// TokenNewWindow represents the NewWindow command token.
	TokenNewWindow TokenType = "NewWindow"
	// TokenCloseWindow represents the CloseWindow command token.
	TokenCloseWindow TokenType = "CloseWindow"
	// TokenNextWindow represents the NextWindow command token.
	TokenNextWindow TokenType = "NextWindow"
	// TokenPrevWindow represents the PrevWindow command token.
	TokenPrevWindow TokenType = "PrevWindow"
	// TokenFocusWindow represents the FocusWindow command token.
	TokenFocusWindow TokenType = "FocusWindow"
	// TokenRenameWindow represents the RenameWindow command token.
	TokenRenameWindow TokenType = "RenameWindow"
	// TokenMinimizeWindow represents the MinimizeWindow command token.
	TokenMinimizeWindow TokenType = "MinimizeWindow"
	// TokenRestoreWindow represents the RestoreWindow command token.
	TokenRestoreWindow TokenType = "RestoreWindow"
	// TokenToggleTiling represents the ToggleTiling command token.
	TokenToggleTiling TokenType = "ToggleTiling"
	// TokenEnableTiling represents the EnableTiling command token.
	TokenEnableTiling TokenType = "EnableTiling"
	// TokenDisableTiling represents the DisableTiling command token.
	TokenDisableTiling TokenType = "DisableTiling"
	// TokenSnapLeft represents the SnapLeft command token.
	TokenSnapLeft TokenType = "SnapLeft"
	// TokenSnapRight represents the SnapRight command token.
	TokenSnapRight TokenType = "SnapRight"
	// TokenSnapFullscreen represents the SnapFullscreen command token.
	TokenSnapFullscreen TokenType = "SnapFullscreen"
	// TokenSwitchWS represents the SwitchWorkspace command token.
	TokenSwitchWS TokenType = "SwitchWorkspace"
	// TokenMoveToWS represents the MoveToWorkspace command token.
	TokenMoveToWS TokenType = "MoveToWorkspace"
	// TokenMoveAndFollowWS represents the MoveAndFollowWorkspace command token.
	TokenMoveAndFollowWS TokenType = "MoveAndFollowWorkspace"
	// TokenSplit represents the Split command token.
	TokenSplit TokenType = "Split"
	// TokenFocus represents the Focus command token.
	TokenFocus TokenType = "Focus"
	// TokenWait represents the Wait command token.
	TokenWait TokenType = "Wait"
	// TokenWaitUntilRegex represents the WaitUntilRegex command token.
	TokenWaitUntilRegex TokenType = "WaitUntilRegex"
	// TokenSet represents the Set command token.
	TokenSet TokenType = "Set"
	// TokenOutput represents the Output command token.
	TokenOutput TokenType = "Output"
	// TokenSource represents the Source command token.
	TokenSource TokenType = "Source"
	// TokenEnableAnimations represents the EnableAnimations command token.
	TokenEnableAnimations TokenType = "EnableAnimations"
	// TokenDisableAnimations represents the DisableAnimations command token.
	TokenDisableAnimations TokenType = "DisableAnimations"
	// TokenToggleAnimations represents the ToggleAnimations command token.
	TokenToggleAnimations TokenType = "ToggleAnimations"
	// TokenTrue represents the true keyword token.
	TokenTrue TokenType = "true"
	// TokenFalse represents the false keyword token.
	TokenFalse TokenType = "false"
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
	case TokenTypeCmd, TokenSleep, TokenEnter, TokenSpace, TokenBackspace,
		TokenDelete, TokenTab, TokenEscape,
		TokenUp, TokenDown, TokenLeft, TokenRight, TokenHome, TokenEnd,
		TokenCtrl, TokenAlt, TokenShift,
		TokenTerminalMode, TokenWindowManagementMode,
		TokenNewWindow, TokenCloseWindow, TokenNextWindow, TokenPrevWindow,
		TokenFocusWindow, TokenRenameWindow, TokenMinimizeWindow, TokenRestoreWindow,
		TokenToggleTiling, TokenEnableTiling, TokenDisableTiling,
		TokenSnapLeft, TokenSnapRight, TokenSnapFullscreen,
		TokenSwitchWS, TokenMoveToWS, TokenMoveAndFollowWS,
		TokenSplit, TokenFocus,
		TokenWait, TokenWaitUntilRegex,
		TokenSet, TokenOutput, TokenSource,
		TokenEnableAnimations, TokenDisableAnimations, TokenToggleAnimations:
		return true
	}
	return false
}

// IsModifier returns true if the token is a modifier key
func (tt TokenType) IsModifier() bool {
	switch tt {
	case TokenCtrl, TokenAlt, TokenShift:
		return true
	}
	return false
}

// IsNavigationKey returns true if the token is a navigation key
func (tt TokenType) IsNavigationKey() bool {
	switch tt {
	case TokenUp, TokenDown, TokenLeft, TokenRight, TokenHome, TokenEnd:
		return true
	}
	return false
}

// KeywordTokenMap maps string keywords to token types
var KeywordTokenMap = map[string]TokenType{
	// Basic commands
	"Type":      TokenTypeCmd,
	"Sleep":     TokenSleep,
	"Enter":     TokenEnter,
	"Space":     TokenSpace,
	"Backspace": TokenBackspace,
	"Delete":    TokenDelete,
	"Tab":       TokenTab,
	"Escape":    TokenEscape,

	// Navigation
	"Up":    TokenUp,
	"Down":  TokenDown,
	"Left":  TokenLeft,
	"Right": TokenRight,
	"Home":  TokenHome,
	"End":   TokenEnd,

	// Modifiers
	"Ctrl":  TokenCtrl,
	"Alt":   TokenAlt,
	"Shift": TokenShift,

	// Mode switching
	"TerminalMode":         TokenTerminalMode,
	"WindowManagementMode": TokenWindowManagementMode,

	// Window management
	"NewWindow":      TokenNewWindow,
	"CloseWindow":    TokenCloseWindow,
	"NextWindow":     TokenNextWindow,
	"PrevWindow":     TokenPrevWindow,
	"FocusWindow":    TokenFocusWindow,
	"RenameWindow":   TokenRenameWindow,
	"MinimizeWindow": TokenMinimizeWindow,
	"RestoreWindow":  TokenRestoreWindow,

	// Tiling
	"ToggleTiling":   TokenToggleTiling,
	"EnableTiling":   TokenEnableTiling,
	"DisableTiling":  TokenDisableTiling,
	"SnapLeft":       TokenSnapLeft,
	"SnapRight":      TokenSnapRight,
	"SnapFullscreen": TokenSnapFullscreen,

	// Workspace
	"SwitchWorkspace":        TokenSwitchWS,
	"MoveToWorkspace":        TokenMoveToWS,
	"MoveAndFollowWorkspace": TokenMoveAndFollowWS,

	// Other actions
	"Split": TokenSplit,
	"Focus": TokenFocus,

	// Synchronization
	"Wait":           TokenWait,
	"WaitUntilRegex": TokenWaitUntilRegex,

	// Settings
	"Set":    TokenSet,
	"Output": TokenOutput,
	"Source": TokenSource,

	// Animations
	"EnableAnimations":  TokenEnableAnimations,
	"DisableAnimations": TokenDisableAnimations,
	"ToggleAnimations":  TokenToggleAnimations,

	// Literals
	"true":  TokenTrue,
	"false": TokenFalse,
}

// keywordTokenMapLower is a lowercase version of KeywordTokenMap for case-insensitive lookup
var keywordTokenMapLower = make(map[string]TokenType)

func init() {
	for k, v := range KeywordTokenMap {
		keywordTokenMapLower[strings.ToLower(k)] = v
	}
}

// LookupKeyword returns the token type for a keyword, or TokenIdentifier if not a keyword
// This function is case-insensitive
func LookupKeyword(ident string) TokenType {
	// Try exact match first
	if tt, ok := KeywordTokenMap[ident]; ok {
		return tt
	}
	// Try case-insensitive match
	if tt, ok := keywordTokenMapLower[strings.ToLower(ident)]; ok {
		return tt
	}
	return TokenIdentifier
}
