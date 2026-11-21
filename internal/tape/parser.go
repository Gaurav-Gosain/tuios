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

func (p *Parser) nextToken() {
	p.curTok = p.peekTok
	p.peekTok = p.lexer.NextToken()
}

// Parse parses the entire tape file and returns all commands
func (p *Parser) Parse() []Command {
	var commands []Command

	for p.curTok.Type != TokenEOF {
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

func (p *Parser) parseCommand() (Command, bool) {
	var cmd Command
	cmd.Line = p.curTok.Line
	cmd.Column = p.curTok.Column

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
	case TokenWait:
		return p.parseWaitCommand()
	case TokenWaitUntilRegex:
		return p.parseWaitUntilRegexCommand()
	case TokenCtrl, TokenAlt, TokenShift:
		return p.parseKeyComboCommand()
	}
	if ct, ok := commandForToken(p.curTok); ok {
		return p.parseGenericCommand(ct)
	}
	p.addError(fmt.Sprintf("unknown command %q. For a keybinding action, write Action and its name", p.curTok.Literal))
	p.skipToNextLine()
	return cmd, false
}

func commandForToken(tok Token) (CommandType, bool) {
	if tok.Type.IsCommand() {
		return CommandType(tok.Type), true
	}
	if tok.Type != TokenIdentifier {
		return "", false
	}
	ct, ok := ResolveCommandName(tok.Literal)
	if !ok || ct == CommandTypeKeyCombo || ct == CommandTypeComment {
		return "", false
	}
	return ct, true
}

func (p *Parser) parseGenericCommand(ct CommandType) (Command, bool) {
	cmd := Command{Type: ct, Line: p.curTok.Line, Column: p.curTok.Column}
	name := p.curTok.Literal
	p.nextToken()

	if p.curTok.Type == TokenAt {
		if !p.parseDelay(&cmd) {
			return cmd, false
		}
	}

	var argToks []Token
	for p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		tok := p.curTok
		switch {
		case tok.Type == TokenIllegal:
			p.addError(fmt.Sprintf("unexpected character %q", tok.Literal))
			p.skipToNextLine()
			return cmd, false
		case tok.Type == TokenPlus && len(cmd.Args) > 0:
			cmd.Args[len(cmd.Args)-1] += "+"
			p.nextToken()
			if p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
				cmd.Args[len(cmd.Args)-1] += p.curTok.Literal
				p.nextToken()
			}
			continue
		}
		cmd.Args = append(cmd.Args, tok.Literal)
		argToks = append(argToks, tok)
		p.nextToken()
	}

	raw := []string{name}
	for i, a := range cmd.Args {
		if argToks[i].Type == TokenString {
			a = fmt.Sprintf("%q", a)
		}
		raw = append(raw, a)
	}
	cmd.Raw = strings.Join(raw, " ")

	if i, err := checkArgs(&cmd); err != nil {
		at := Token{Line: cmd.Line, Column: cmd.Column}
		if i >= 0 && i < len(argToks) {
			at = argToks[i]
		}
		p.addErrorAt(at, err.Error())
		return cmd, false
	}
	if cmd.Delay > 0 && !takesDelay(ct) {
		p.addErrorAt(Token{Line: cmd.Line, Column: cmd.Column}, fmt.Sprintf("%s does not take an @ delay. It works on Type and on the key commands, such as Down@100ms 3", ct))
		return cmd, false
	}
	return cmd, true
}

func (p *Parser) parseDelay(cmd *Command) bool {
	p.nextToken()
	if p.curTok.Type != TokenDuration {
		p.addError("expected a duration after @, as in Type@50ms")
		p.skipToNextLine()
		return false
	}
	d, err := ParseDuration(p.curTok.Literal)
	if err != nil || d <= 0 {
		p.addError(fmt.Sprintf("invalid duration: %s", p.curTok.Literal))
		p.skipToNextLine()
		return false
	}
	cmd.Delay = d
	p.nextToken()
	return true
}

func takesDelay(ct CommandType) bool {
	if ct == CommandTypeType {
		return true
	}
	spec, ok := argSpecs[ct]
	return ok && spec.usage == keyRepeat.usage
}

func (p *Parser) parseTypeCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeType,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken()

	if p.curTok.Type == TokenAt && !p.parseDelay(&cmd) {
		return cmd, false
	}

	if p.curTok.Type == TokenString {
		cmd.Args = []string{p.curTok.Literal}
		cmd.Raw = fmt.Sprintf("Type %q", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("Type command expects a string, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	return cmd, p.endOfLine(cmd.Type)
}

func (p *Parser) parseSleepCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeSleep,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken()

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

	return cmd, p.endOfLine(cmd.Type)
}

func (p *Parser) parseKeyComboCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeKeyCombo,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	var comboParts []string

	for p.curTok.Type == TokenCtrl || p.curTok.Type == TokenAlt || p.curTok.Type == TokenShift {
		comboParts = append(comboParts, p.curTok.Literal)
		p.nextToken()

		if p.curTok.Type == TokenPlus {
			p.nextToken()
		}
	}

	if p.curTok.Type == TokenIdentifier || p.curTok.Type.IsNavigationKey() ||
		p.curTok.Type == TokenEnter || p.curTok.Type == TokenSpace ||
		p.curTok.Type == TokenTab || p.curTok.Type == TokenEscape ||
		p.curTok.Type == TokenBackspace || p.curTok.Type == TokenDelete ||
		p.curTok.Type == TokenNumber ||
		(len(p.curTok.Literal) > 0 && isDigit(p.curTok.Literal[0])) {
		comboParts = append(comboParts, p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("expected key after modifier, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	comboStr := strings.Join(comboParts, "+")
	cmd.Args = []string{comboStr}
	cmd.Raw = comboStr

	return cmd, p.endOfLine(cmd.Type)
}

func (p *Parser) parseWaitCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeWait,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken()

	if p.curTok.Type == TokenDuration {
		duration, err := ParseDuration(p.curTok.Literal)
		if err != nil {
			p.addError(fmt.Sprintf("invalid duration: %s", p.curTok.Literal))
		}
		cmd.Args = []string{p.curTok.Literal}
		cmd.Delay = duration
		cmd.Raw = fmt.Sprintf("Wait %s", p.curTok.Literal)
		p.nextToken()
	} else {
		p.addError(fmt.Sprintf("Wait command expects a duration, got %v", p.curTok.Type))
		p.skipToNextLine()
		return cmd, false
	}

	return cmd, p.endOfLine(cmd.Type)
}

func (p *Parser) parseWaitUntilRegexCommand() (Command, bool) {
	cmd := Command{
		Type:   CommandTypeWaitUntilRegex,
		Line:   p.curTok.Line,
		Column: p.curTok.Column,
	}

	p.nextToken()

	if p.curTok.Type == TokenString {
		regexPattern := p.curTok.Literal
		cmd.Args = []string{regexPattern}
		p.nextToken()

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

	return cmd, p.endOfLine(cmd.Type)
}

func (p *Parser) skipToNextLine() {
	for p.curTok.Type != TokenNewline && p.curTok.Type != TokenEOF {
		p.nextToken()
	}
}

func (p *Parser) endOfLine(ct CommandType) bool {
	if p.curTok.Type == TokenNewline || p.curTok.Type == TokenEOF {
		return true
	}
	p.addError(fmt.Sprintf("unexpected %q after %s", p.curTok.Literal, ct))
	p.skipToNextLine()
	return false
}

func (p *Parser) addError(msg string) {
	p.addErrorAt(p.curTok, msg)
}

func (p *Parser) addErrorAt(tok Token, msg string) {
	p.errors = append(p.errors, fmt.Sprintf("line %d, column %d: %s", tok.Line, tok.Column, msg))
}

// Errors returns the list of parser errors
func (p *Parser) Errors() []string {
	return p.errors
}

// ParseFile parses a tape file from a string. An @ delay is expanded here into
// the commands it stands for, so every player runs it the same way.
func ParseFile(content string) ([]Command, []string) {
	l := New(content)
	p := NewParser(l)
	commands := p.Parse()
	return expandDelays(commands), p.Errors()
}

func expandDelays(commands []Command) []Command {
	var out []Command
	for _, c := range commands {
		if c.Delay <= 0 || c.Type == CommandTypeSleep || c.Type == CommandTypeWait {
			out = append(out, c)
			continue
		}
		var steps []Command
		switch {
		case c.Type == CommandTypeType && len(c.Args) == 1:
			for _, r := range c.Args[0] {
				step := c
				step.Args = []string{string(r)}
				steps = append(steps, step)
			}
		default:
			step := c
			step.Args = nil
			for range repeatCount(&c) {
				steps = append(steps, step)
			}
		}
		for i, step := range steps {
			step.Delay = 0
			if i > 0 {
				out = append(out, Command{Type: CommandTypeSleep, Delay: c.Delay, Args: []string{c.Delay.String()},
					Line: c.Line, Column: c.Column, File: c.File, Raw: "Sleep " + c.Delay.String()})
			}
			out = append(out, step)
		}
	}
	return out
}
