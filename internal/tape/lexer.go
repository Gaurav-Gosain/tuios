package tape

import (
	"strings"
	"unicode"
)

// Lexer tokenizes .tape file input
type Lexer struct {
	input  string
	pos    int    // current position
	nextPos int   // next position
	ch     byte   // current character
	line   int    // current line
	column int    // current column
}

// New creates a new Lexer for the given input
func New(input string) *Lexer {
	l := &Lexer{
		input:  input,
		pos:    0,
		nextPos: 0,
		line:   1,
		column: 0,
	}
	l.readChar()
	return l
}

// readChar reads the next character and updates position tracking
func (l *Lexer) readChar() {
	if l.nextPos >= len(l.input) {
		l.ch = 0 // EOF
	} else {
		l.ch = l.input[l.nextPos]
	}

	if l.nextPos > 0 && l.ch == '\n' {
		l.line++
		l.column = 0
	}

	l.pos = l.nextPos
	l.nextPos++
	l.column++
}

// peekChar returns the next character without consuming it
func (l *Lexer) peekChar() byte {
	if l.nextPos >= len(l.input) {
		return 0
	}
	return l.input[l.nextPos]
}

// peekCharN peeks n characters ahead
func (l *Lexer) peekCharN(n int) byte {
	pos := l.nextPos + n - 1
	if pos >= len(l.input) {
		return 0
	}
	return l.input[pos]
}

// skipWhitespace skips spaces and tabs (not newlines)
func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
		l.readChar()
	}
}

// skipComment skips a comment line (from # to end of line)
func (l *Lexer) skipComment() {
	for l.ch != '\n' && l.ch != 0 {
		l.readChar()
	}
}

// readString reads a quoted string (single, double, or backtick)
func (l *Lexer) readString(quote byte) string {
	var sb strings.Builder
	l.readChar() // skip opening quote

	for l.ch != quote && l.ch != 0 {
		if l.ch == '\\' {
			l.readChar()
			switch l.ch {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			case '\'':
				sb.WriteByte('\'')
			case '`':
				sb.WriteByte('`')
			default:
				sb.WriteByte(l.ch)
			}
		} else {
			sb.WriteByte(l.ch)
		}
		l.readChar()
	}

	if l.ch == quote {
		l.readChar() // skip closing quote
	}

	return sb.String()
}

// readIdentifier reads an identifier or keyword
func (l *Lexer) readIdentifier() string {
	var sb strings.Builder
	for isIdentifierChar(l.ch) {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	return sb.String()
}

// readNumber reads a number literal
func (l *Lexer) readNumber() string {
	var sb strings.Builder
	for isDigit(l.ch) {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	return sb.String()
}

// readDuration reads a duration literal (e.g., 500ms, 1s, 2.5s)
func (l *Lexer) readDuration() string {
	var sb strings.Builder
	for isDigit(l.ch) || l.ch == '.' {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	// Read the unit (ms, s, etc.)
	for unicode.IsLetter(rune(l.ch)) {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	return sb.String()
}

// readRegex reads a regex pattern /pattern/
func (l *Lexer) readRegex() string {
	var sb strings.Builder
	l.readChar() // skip opening /

	for l.ch != '/' && l.ch != 0 {
		if l.ch == '\\' {
			sb.WriteByte(l.ch)
			l.readChar()
			if l.ch != 0 {
				sb.WriteByte(l.ch)
				l.readChar()
			}
		} else {
			sb.WriteByte(l.ch)
			l.readChar()
		}
	}

	if l.ch == '/' {
		l.readChar() // skip closing /
	}

	return sb.String()
}

// NextToken returns the next token in the input
func (l *Lexer) NextToken() Token {
	var tok Token
	tok.Line = l.line
	tok.Column = l.column

	l.skipWhitespace()

	switch l.ch {
	case 0:
		tok.Type = TOKEN_EOF
		tok.Literal = ""

	case '\n':
		tok.Type = TOKEN_NEWLINE
		tok.Literal = "\n"
		l.readChar()

	case '#':
		l.skipComment()
		return l.NextToken() // Skip comments and get next token

	case '+':
		tok.Type = TOKEN_PLUS
		tok.Literal = "+"
		l.readChar()

	case '@':
		tok.Type = TOKEN_AT
		tok.Literal = "@"
		l.readChar()

	case ',':
		tok.Type = TOKEN_COMMA
		tok.Literal = ","
		l.readChar()

	case '/':
		// Could be division or regex - peek ahead
		if l.peekChar() == '/' || isIdentifierChar(l.peekChar()) {
			// Likely regex for Wait command
			regex := l.readRegex()
			tok.Type = TOKEN_SLASH
			tok.Literal = regex
		} else {
			tok.Type = TOKEN_SLASH
			tok.Literal = "/"
			l.readChar()
		}

	case '(':
		tok.Type = TOKEN_LPAREN
		tok.Literal = "("
		l.readChar()

	case ')':
		tok.Type = TOKEN_RPAREN
		tok.Literal = ")"
		l.readChar()

	case '"', '\'', '`':
		quote := l.ch
		literal := l.readString(quote)
		tok.Type = TOKEN_STRING
		tok.Literal = literal

	default:
		if isDigit(l.ch) {
			// Could be a number or duration
			num := l.readNumber()

			// Check if it's a duration (has a letter after the number)
			if unicode.IsLetter(rune(l.ch)) {
				unit := ""
				for unicode.IsLetter(rune(l.ch)) {
					unit += string(l.ch)
					l.readChar()
				}
				tok.Type = TOKEN_DURATION
				tok.Literal = num + unit
			} else {
				tok.Type = TOKEN_NUMBER
				tok.Literal = num
			}
		} else if isIdentifierChar(l.ch) {
			literal := l.readIdentifier()
			tok.Type = LookupKeyword(literal)
			tok.Literal = literal
		} else {
			tok.Type = TOKEN_ILLEGAL
			tok.Literal = string(l.ch)
			l.readChar()
		}
	}

	return tok
}

// isDigit returns true if ch is a digit
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// isIdentifierChar returns true if ch is valid in an identifier
func isIdentifierChar(ch byte) bool {
	return unicode.IsLetter(rune(ch)) || isDigit(ch) || ch == '_'
}

// Tokenize returns all tokens from the input (useful for testing)
func Tokenize(input string) []Token {
	l := New(input)
	var tokens []Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == TOKEN_EOF {
			break
		}
	}
	return tokens
}
