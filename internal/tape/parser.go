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

	for p.curTok.Type != TokenEOF {
		// Skip newlines
		if p.curTok.Type == TokenNewline {
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
	for p.curTok.Type == TokenNewline {
		p.nextToken()
	}

	if p.curTok.Type == TokenEOF {
		return cmd, false
	}

	switch p.curTok.Type {
	case TokenTypeCmd:
		return p.parseTypeCommand()
	case TokenSleep:
		return p.parseSleepCommand()
	case TokenEnter:
		return p.parseBasicCommand(CommandTypeEnter)
	case TokenSpace:
		return p.parseBasicCommand(CommandTypeSpace)
	case TokenBackspace:
		return p.parseBasicCommand(CommandTypeBackspace)
	case TokenDelete:
		return p.parseBasicCommand(CommandTypeDelete)
	case TokenTab:
		return p.parseBasicCommand(CommandTypeTab)
	case TokenEscape:
		return p.parseBasicCommand(CommandTypeEscape)
	case TokenUp:
		return p.parseBasicCommand(CommandTypeUp)
	case TokenDown:
		return p.parseBasicCommand(CommandTypeDown)
	case TokenLeft:
		return p.parseBasicCommand(CommandTypeLeft)
	case TokenRight:
		return p.parseBasicCommand(CommandTypeRight)
	case TokenHome:
		return p.parseBasicCommand(CommandTypeHome)
	case TokenEnd:
		return p.parseBasicCommand(CommandTypeEnd)
	case TokenCtrl, TokenAlt, TokenShift:
		return p.parseKeyComboCommand()
	case TokenTerminalMode:
		return p.parseBasicCommand(CommandTypeTerminalMode)
	case TokenWindowManagementMode:
		return p.parseBasicCommand(CommandTypeWindowManagementMode)
	case TokenNewWindow:
		return p.parseBasicCommand(CommandTypeNewWindow)
	case TokenCloseWindow:
		return p.parseBasicCommand(CommandTypeCloseWindow)
	case TokenNextWindow:
		return p.parseBasicCommand(CommandTypeNextWindow)
	case TokenPrevWindow:
		return p.parseBasicCommand(CommandTypePrevWindow)
	case TokenFocusWindow:
		return p.parseWindowIDCommand(CommandTypeFocusWindow)
	case TokenRenameWindow:
		return p.parseWindowRenameCommand()
	case TokenMinimizeWindow:
		return p.parseBasicCommand(CommandTypeMinimizeWindow)
	case TokenRestoreWindow:
		return p.parseBasicCommand(CommandTypeRestoreWindow)
	case TokenToggleTiling:
		return p.parseBasicCommand(CommandTypeToggleTiling)
	case TokenEnableTiling:
		return p.parseBasicCommand(CommandTypeEnableTiling)
	case TokenDisableTiling:
		return p.parseBasicCommand(CommandTypeDisableTiling)
	case TokenSnapLeft:
		return p.parseBasicCommand(CommandTypeSnapLeft)
	case TokenSnapRight:
		return p.parseBasicCommand(CommandTypeSnapRight)
	case TokenSnapFullscreen:
		return p.parseBasicCommand(CommandTypeSnapFullscreen)
	case TokenSwitchWS:
		return p.parseSwitchWorkspaceCommand()
	case TokenMoveToWS:
		return p.parseMoveToWorkspaceCommand()
	case TokenMoveAndFollowWS:
		return p.parseMoveAndFollowWorkspaceCommand()
	case TokenSplit:
		return p.parseBasicCommand(CommandTypeSplit)
	case TokenFocus:
		return p.parseFocusCommand()
	case TokenWait:
		return p.parseWaitCommand()
	case TokenWaitUntilRegex:
		return p.parseWaitUntilRegexCommand()
	case TokenSet:
		return p.parseSetCommand()
	case TokenOutput:
		return p.parseOutputCommand()
	case TokenSource:
		return p.parseSourceCommand()
	case TokenEnableAnimations:
		return p.parseBasicCommand(CommandTypeEnableAnimations)
	case TokenDisableAnimations:
		return p.parseBasicCommand(CommandTypeDisableAnimations)
	case TokenToggleAnimations:
		return p.parseBasicCommand(CommandTypeToggleAnimations)
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
	if p.curTok.Type == TokenAt {
		p.nextToken()
		if p.curTok.Type == TokenDuration {
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
	if p.curTok.Type == TokenNumber {
		cmd.Args = append(cmd.Args, p.curTok.Literal)
		p.nextToken()
	}

	cmd.Raw = cmdName
	skipToNextLine := p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF
	if skipToNextLine {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseTypeCommand parses Type "text" commands
func (p *Parser) parseTypeCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeType,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Type

	// Check for optional speed modifier (@<duration>)
	if p.curTok.Type == TokenAt {
		p.nextToken()
		if p.curTok.Type == TokenDuration {
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
	if p.curTok.Type == TokenString {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Type %q", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("Type command expects a string, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSleepCommand parses Sleep <duration> commands
func (p *Parser) parseSleepCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeSleep,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Sleep

	if p.curTok.Type == TokenDuration {
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

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseKeyComboCommand parses Ctrl+X, Alt+X, etc.
func (p *Parser) parseKeyComboCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeKeyCombo,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	var comboParts []string

	// Parse Ctrl, Alt, Shift modifiers and their keys
	for p.curTok.Type == TokenCtrl || p.curTok.Type == TokenAlt || p.curTok.Type == TokenShift {
		comboParts = append(comboParts, p.curTok.Literal)
		p.nextToken()

		// Expect + after each modifier
		if p.curTok.Type == TokenPlus {
			p.nextToken()
		}
	}

	// Get the final key
	if p.curTok.Type == TokenIdentifier || p.curTok.Type.IsNavigationKey() ||
		p.curTok.Type == TokenEnter || p.curTok.Type == TokenSpace ||
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

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseFocusCommand parses Focus <target> commands
func (p *Parser) parseFocusCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeFocus,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Focus

	if p.curTok.Type == TokenIdentifier || p.curTok.Type == TokenNumber {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Focus %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("Focus command expects an identifier or number")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSwitchWorkspaceCommand parses SwitchWorkspace <n> or Alt+N commands
func (p *Parser) parseSwitchWorkspaceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeSwitchWS,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume SwitchWorkspace

	if p.curTok.Type == TokenNumber {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("SwitchWorkspace %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("SwitchWorkspace expects a number, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseMoveToWorkspaceCommand parses MoveToWorkspace <n> commands
func (p *Parser) parseMoveToWorkspaceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeMoveToWS,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume MoveToWorkspace

	if p.curTok.Type == TokenNumber {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("MoveToWorkspace %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("MoveToWorkspace expects a number, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseMoveAndFollowWorkspaceCommand parses MoveAndFollowWorkspace <n> commands
func (p *Parser) parseMoveAndFollowWorkspaceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeMoveAndFollowWS,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume MoveAndFollowWorkspace

	if p.curTok.Type == TokenNumber {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("MoveAndFollowWorkspace %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("MoveAndFollowWorkspace expects a number, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
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

	switch p.curTok.Type {
	case TokenIdentifier, TokenNumber:
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("%s %s", cmdType, p.curTok.Literal)
		p.nextToken()
	default:
		p.addError(fmt.Sprintf("%s expects a window ID, got %v", cmdType, p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseWindowRenameCommand parses RenameWindow <name> commands
func (p *Parser) parseWindowRenameCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeRenameWindow,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume RenameWindow

	switch p.curTok.Type {
	case TokenString:
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("RenameWindow %q", p.curTok.Literal)
		p.nextToken()
	case TokenIdentifier:
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("RenameWindow %s", p.curTok.Literal)
		p.nextToken()
	default:
		p.addError("RenameWindow expects a window name")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseWaitCommand parses Wait commands (for future use)
func (p *Parser) parseWaitCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeWait,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Wait

	// Collect all arguments until newline
	for p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
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
		Type:   CommandTypeWaitUntilRegex,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume WaitUntilRegex

	// Get regex pattern (must be a string)
	if p.curTok.Type == TokenString {
		regexPattern := p.curTok.Literal
		cmd.Args = []string{regexPattern}
		p.nextToken()

		// Optional timeout parameter
		if p.curTok.Type == TokenNumber {
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

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSetCommand parses Set <key> <value> commands
func (p *Parser) parseSetCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeSet,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Set

	// Get key
	if p.curTok.Type == TokenIdentifier {
		key := p.curTok.Literal
		p.nextToken()

		// Get value
		if p.curTok.Type == TokenIdentifier || p.curTok.Type == TokenString ||
			p.curTok.Type == TokenNumber || p.curTok.Type == TokenDuration {
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

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseOutputCommand parses Output <file> commands
func (p *Parser) parseOutputCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeOutput,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Output

	if p.curTok.Type == TokenString || p.curTok.Type == TokenIdentifier {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Output %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("Output command expects a filename")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// parseSourceCommand parses Source <file> commands
func (p *Parser) parseSourceCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeSource,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken() // consume Source

	if p.curTok.Type == TokenString || p.curTok.Type == TokenIdentifier {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Source %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError("Source command expects a filename")
		p.skipToNextLine()
		return cmd, false
	}

	if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.skipToNextLine()
	}

	return cmd, true
}

// skipToNextLine skips tokens until the next newline
func (p *Parser) skipToNextLine() {
	for p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
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
