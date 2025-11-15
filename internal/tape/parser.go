package tape

import (
	"fmt"
	"strings"
)

// Parser parses .tape files into commands
type Parser struct {
	lexer   *Lexer
	curTok  Token
	peekTok Token
	errors  []string
}

// NewParser creates a new parser from a lexer
func NewParser(l *Lexer) *Parser {
	p := &Parser{
		lexer:  l,
		errors: []string{},
	}
	p.nextToken()
	p.nextToken()
	return p
}

// nextToken advances to the next token
func (p *Parser) nextToken() {
	p.curTok = p.peekTok
	p.peekTok = p.lexer.NextToken()
}

// Parse parses the entire tape file and returns all commands
func (p *Parser) Parse() []Command {
	var commands []Command

	for p.curTok.Type != TOKEN_EOF {
		// Skip newlines
		if p.curTok.Type == TOKEN_NEWLINE {
			p.nextToken()
			continue
		}

		cmd, ok := p.parseCommand()
		if !ok {
			p.nextToken()
			continue
		}

		commands = append(commands, cmd)
	}

	return commands
}

// parseCommand parses a single command
func (p *Parser) parseCommand() (Command, bool) {
	var cmd Command
	cmd.Line = p.curTok.Line
	cmd.Column = p.curTok.Column

	// Skip any leading newlines
	for p.curTok.Type == TOKEN_NEWLINE {
		p.nextToken()
	}

	if p.curTok.Type == TOKEN_EOF {
		return cmd, false
	}

	tt := p.curTok.Type

	switch {
	case tt == TOKEN_TYPE:
		return p.parseTypeCommand()
	case tt == TOKEN_SLEEP:
		return p.parseSleepCommand()
	case tt == TOKEN_ENTER:
		return p.parseBasicCommand(CommandType_Enter)
	case tt == TOKEN_SPACE:
		return p.parseBasicCommand(CommandType_Space)
	case tt == TOKEN_BACKSPACE:
		return p.parseBasicCommand(CommandType_Backspace)
	case tt == TOKEN_DELETE:
		return p.parseBasicCommand(CommandType_Delete)
	case tt == TOKEN_TAB:
		return p.parseBasicCommand(CommandType_Tab)
	case tt == TOKEN_ESCAPE:
		return p.parseBasicCommand(CommandType_Escape)
	case tt == TOKEN_UP:
		return p.parseBasicCommand(CommandType_Up)
	case tt == TOKEN_DOWN:
		return p.parseBasicCommand(CommandType_Down)
	case tt == TOKEN_LEFT:
		return p.parseBasicCommand(CommandType_Left)
	case tt == TOKEN_RIGHT:
		return p.parseBasicCommand(CommandType_Right)
	case tt == TOKEN_HOME:
		return p.parseBasicCommand(CommandType_Home)
	case tt == TOKEN_END:
		return p.parseBasicCommand(CommandType_End)
	case tt == TOKEN_CTRL, tt == TOKEN_ALT, tt == TOKEN_SHIFT:
		return p.parseKeyComboCommand()
	case tt == TOKEN_TERMINAL_MODE:
		return p.parseBasicCommand(CommandType_TerminalMode)
	case tt == TOKEN_WINDOW_MANAGEMENT_MODE:
		return p.parseBasicCommand(CommandType_WindowManagementMode)
	case tt == TOKEN_NEW_WINDOW:
		return p.parseBasicCommand(CommandType_NewWindow)
	case tt == TOKEN_CLOSE_WINDOW:
		return p.parseBasicCommand(CommandType_CloseWindow)
	case tt == TOKEN_NEXT_WINDOW:
		return p.parseBasicCommand(CommandType_NextWindow)
	case tt == TOKEN_PREV_WINDOW:
		return p.parseBasicCommand(CommandType_PrevWindow)
	case tt == TOKEN_FOCUS_WINDOW:
		return p.parseWindowIDCommand(CommandType_FocusWindow)
	case tt == TOKEN_RENAME_WINDOW:
		return p.parseWindowRenameCommand()
	case tt == TOKEN_MINIMIZE_WINDOW:
		return p.parseBasicCommand(CommandType_MinimizeWindow)
	case tt == TOKEN_RESTORE_WINDOW:
		return p.parseBasicCommand(CommandType_RestoreWindow)
	case tt == TOKEN_TOGGLE_TILING:
		return p.parseBasicCommand(CommandType_ToggleTiling)
	case tt == TOKEN_ENABLE_TILING:
		return p.parseBasicCommand(CommandType_EnableTiling)
	case tt == TOKEN_DISABLE_TILING:
		return p.parseBasicCommand(CommandType_DisableTiling)
	case tt == TOKEN_SNAP_LEFT:
		return p.parseBasicCommand(CommandType_SnapLeft)
	case tt == TOKEN_SNAP_RIGHT:
		return p.parseBasicCommand(CommandType_SnapRight)
	case tt == TOKEN_SNAP_FULLSCREEN:
		return p.parseBasicCommand(CommandType_SnapFullscreen)
	case tt == TOKEN_SWITCH_WS:
		return p.parseSwitchWorkspaceCommand()
	case tt == TOKEN_MOVE_TO_WS:
		return p.parseMoveToWorkspaceCommand()
	case tt == TOKEN_MOVE_AND_FOLLOW_WS:
		return p.parseMoveAndFollowWorkspaceCommand()
	case tt == TOKEN_SPLIT:
		return p.parseBasicCommand(CommandType_Split)
	case tt == TOKEN_FOCUS:
		return p.parseFocusCommand()
	case tt == TOKEN_WAIT:
		return p.parseWaitCommand()
	case tt == TOKEN_WAIT_UNTIL_REGEX:
		return p.parseWaitUntilRegexCommand()
	case tt == TOKEN_SET:
		return p.parseSetCommand()
	case tt == TOKEN_OUTPUT:
		return p.parseOutputCommand()
	case tt == TOKEN_SOURCE:
		return p.parseSourceCommand()
	default:
		p.addError(fmt.Sprintf("unexpected token: %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}
}

// parseBasicCommand parses simple commands with optional repeat count
func (p *Parser) parseBasicCommand(cmdType CommandType) (Command, bool) {
	cmd := Command{
		Type:   cmdType,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	cmdName := p.curTok.Literal
	p.nextToken()

	// Check for optional delay modifier (@<duration>)
	if p.curTok.Type == TOKEN_AT {
		p.nextToken()
		if p.curTok.Type == TOKEN_DURATION {
			duration, err := ParseDuration(p.curTok.Literal)
			if err != nil {
				p.addError(fmt.Sprintf("invalid duration: %s", p.curTok.Literal))
			}
			cmd.Delay = duration
			p.nextToken()
		} else {
			p.addError("expected duration after @")
		}
	}

	// Check for optional repeat count (number)
	if p.curTok.Type == TOKEN_NUMBER {
		cmd.Args = append(cmd.Args, p.curTok.Literal)
		p.nextToken()
	}

	cmd.Raw = cmdName
	skipToNextLine := p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF
	if skipToNextLine {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseTypeCommand parses Type "text" commands
func (p *Parser) parseTypeCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Type,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Type

	// Check for optional speed modifier (@<duration>)
	if p.curTok.Type == TOKEN_AT {
		p.nextToken()
		if p.curTok.Type == TOKEN_DURATION {
			duration, err := ParseDuration(p.curTok.Literal)
			if err != nil {
				p.addError(fmt.Sprintf("invalid duration: %s", p.curTok.Literal))
			}
			cmd.Delay = duration
			p.nextToken()
		} else {
			p.addError("expected duration after @")
		}
	}

	// Expect a string argument
	if p.curTok.Type == TOKEN_STRING {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Type %q", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("Type command expects a string, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSleepCommand parses Sleep <duration> commands
func (p *Parser) parseSleepCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Sleep,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Sleep

	if p.curTok.Type == TOKEN_DURATION {
		duration, err := ParseDuration(p.curTok.Literal)
		if err != nil {
			p.addError(fmt.Sprintf("invalid duration: %s", p.curTok.Literal))
		}
		cmd.Args = []string{p.curTok.Literal}
		cmd.Delay = duration
		cmd.Raw = fmt.Sprintf("Sleep %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("Sleep command expects a duration, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseKeyComboCommand parses Ctrl+X, Alt+X, etc.
func (p *Parser) parseKeyComboCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_KeyCombo,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	var comboParts []string

	// Parse Ctrl, Alt, Shift modifiers and their keys
	for p.curTok.Type == TOKEN_CTRL || p.curTok.Type == TOKEN_ALT || p.curTok.Type == TOKEN_SHIFT {
		comboParts = append(comboParts, p.curTok.Literal)
		p.nextToken()

		// Expect + after each modifier
		if p.curTok.Type == TOKEN_PLUS {
			p.nextToken()
		}
	}

	// Get the final key
	if p.curTok.Type == TOKEN_IDENTIFIER || p.curTok.Type.IsNavigationKey() ||
		p.curTok.Type == TOKEN_ENTER || p.curTok.Type == TOKEN_SPACE ||
		isDigit(p.curTok.Literal[0]) {
		comboParts = append(comboParts, p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("expected key after modifier, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	// Reconstruct the combo string
	comboStr := strings.Join(comboParts, "+")
	cmd.Args = []string{comboStr}
	cmd.Raw = comboStr

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseFocusCommand parses Focus <target> commands
func (p *Parser) parseFocusCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Focus,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Focus

	if p.curTok.Type == TOKEN_IDENTIFIER || p.curTok.Type == TOKEN_NUMBER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Focus %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("Focus command expects an identifier or number")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSwitchWorkspaceCommand parses SwitchWorkspace <n> or Alt+N commands
func (p *Parser) parseSwitchWorkspaceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_SwitchWS,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume SwitchWorkspace

	if p.curTok.Type == TOKEN_NUMBER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("SwitchWorkspace %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("SwitchWorkspace expects a number, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseMoveToWorkspaceCommand parses MoveToWorkspace <n> commands
func (p *Parser) parseMoveToWorkspaceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_MoveToWS,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume MoveToWorkspace

	if p.curTok.Type == TOKEN_NUMBER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("MoveToWorkspace %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("MoveToWorkspace expects a number, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseMoveAndFollowWorkspaceCommand parses MoveAndFollowWorkspace <n> commands
func (p *Parser) parseMoveAndFollowWorkspaceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_MoveAndFollowWS,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume MoveAndFollowWorkspace

	if p.curTok.Type == TOKEN_NUMBER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("MoveAndFollowWorkspace %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("MoveAndFollowWorkspace expects a number, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseWindowIDCommand parses commands that take a window ID like FocusWindow <id>
func (p *Parser) parseWindowIDCommand(cmdType CommandType) (Command, bool) {
	cmd := Command{
		Type:   cmdType,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume command name

	if p.curTok.Type == TOKEN_IDENTIFIER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("%s %s", cmdType, p.curTok.Literal)
		p.nextToken()
	} else if p.curTok.Type == TOKEN_NUMBER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("%s %s", cmdType, p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("%s expects a window ID, got %v", cmdType, p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseWindowRenameCommand parses RenameWindow <name> commands
func (p *Parser) parseWindowRenameCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_RenameWindow,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume RenameWindow

	if p.curTok.Type == TOKEN_STRING {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("RenameWindow %q", p.curTok.Literal)
		p.nextToken()
	} else if p.curTok.Type == TOKEN_IDENTIFIER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("RenameWindow %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("RenameWindow expects a window name")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseWaitCommand parses Wait commands (for future use)
func (p *Parser) parseWaitCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Wait,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Wait

	// Collect all arguments until newline
	for p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		cmd.Args = append(cmd.Args, p.curTok.Literal)
		p.nextToken()
	}

	return cmd, true
}

// parseWaitUntilRegexCommand parses WaitUntilRegex <regex> [timeout] commands
// WaitUntilRegex will wait until the PTY output matches the given regex pattern
// Optional timeout in milliseconds (default: 5000ms)
func (p *Parser) parseWaitUntilRegexCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_WaitUntilRegex,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume WaitUntilRegex

	// Get regex pattern (must be a string)
	if p.curTok.Type == TOKEN_STRING {
		regexPattern := p.curTok.Literal
		cmd.Args = []string{regexPattern}
		p.nextToken()

		// Optional timeout parameter
		if p.curTok.Type == TOKEN_NUMBER {
			cmd.Args = append(cmd.Args, p.curTok.Literal)
			cmd.Raw = fmt.Sprintf("WaitUntilRegex %q %s", regexPattern, p.curTok.Literal)
			p.nextToken()
		} else {
			cmd.Raw = fmt.Sprintf("WaitUntilRegex %q", regexPattern)
		}
	} else {
		p.addError("WaitUntilRegex expects a regex pattern string")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSetCommand parses Set <key> <value> commands
func (p *Parser) parseSetCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Set,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Set

	// Get key
	if p.curTok.Type == TOKEN_IDENTIFIER {
		key := p.curTok.Literal
		p.nextToken()

		// Get value
		if p.curTok.Type == TOKEN_IDENTIFIER || p.curTok.Type == TOKEN_STRING ||
			p.curTok.Type == TOKEN_NUMBER || p.curTok.Type == TOKEN_DURATION {
			value := p.curTok.Literal
			cmd.Args = []string{key, value}
			cmd.Raw = fmt.Sprintf("Set %s %s", key, value)
			p.nextToken()
		} else {
			p.addError("Set command expects a value")
			p.skipToNextLine()
			return cmd, false
		}
	} else {
		p.addError("Set command expects a key")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseOutputCommand parses Output <file> commands
func (p *Parser) parseOutputCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Output,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Output

	if p.curTok.Type == TOKEN_STRING || p.curTok.Type == TOKEN_IDENTIFIER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Output %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("Output command expects a filename")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSourceCommand parses Source <file> commands
func (p *Parser) parseSourceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandType_Source,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Source

	if p.curTok.Type == TOKEN_STRING || p.curTok.Type == TOKEN_IDENTIFIER {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Source %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("Source command expects a filename")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// skipToNextLine skips tokens until the next newline
func (p *Parser) skipToNextLine() {
	for p.curTok.Type != TOKEN_NEWLINE && p.curTok.Type != TOKEN_EOF {
		p.nextToken()
	}
}

// addError adds an error to the parser's error list
func (p *Parser) addError(msg string) {
	p.errors = append(p.errors, fmt.Sprintf("line %d: %s", p.curTok.Line, msg))
}

// Errors returns the list of parser errors
func (p *Parser) Errors() []string {
	return p.errors
}

// ParseFile parses a tape file from a string
func ParseFile(content string) ([]Command, []string) {
	l := New(content)
	p := NewParser(l)
	commands := p.Parse()
	return commands, p.Errors()
}

