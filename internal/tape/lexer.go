package tape

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer tokenizes .tape file input
type Lexer struct {
	input   string
	pos     int
	nextPos int
	ch      byte
	line    int
	column  int
}

// New creates a new Lexer for the given input
func New(input string) *Lexer {
	l := &Lexer{
		input:   input,
		pos:     0,
		nextPos: 0,
		line:    1,
		column:  0,
	}
	l.readChar()
	return l
}

func (l *Lexer) readChar() {
	if l.pos < l.nextPos {
		if l.ch == '\n' {
			l.line++
			l.column = 1
		} else {
			l.column++
		}
	} else {
		l.column = 1
	}

	if l.nextPos >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.nextPos]
	}

	l.pos = l.nextPos
	l.nextPos++
}

func (l *Lexer) peekChar() byte {
	if l.nextPos >= len(l.input) {
		return 0
	}
	return l.input[l.nextPos]
}

func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
		l.readChar()
	}
}

func (l *Lexer) skipComment() {
	for l.ch != '\n' && l.ch != 0 {
		l.readChar()
	}
}

func (l *Lexer) readString(quote byte) string {
	var sb strings.Builder
	l.readChar()

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
			case 'a':
				sb.WriteByte('\a')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'v':
				sb.WriteByte('\v')
			case 'x', 'u', 'U':
				if !l.readHexEscape(&sb) {
					sb.WriteByte(l.ch)
				}
			default:
				sb.WriteByte(l.ch)
			}
		} else {
			sb.WriteByte(l.ch)
		}
		l.readChar()
	}

	if l.ch == quote {
		l.readChar()
	}

	return sb.String()
}

func (l *Lexer) readHexEscape(sb *strings.Builder) bool {
	n := map[byte]int{'x': 2, 'u': 4, 'U': 8}[l.ch]
	if l.nextPos+n > len(l.input) {
		return false
	}
	v, err := strconv.ParseUint(l.input[l.nextPos:l.nextPos+n], 16, 32)
	if err != nil {
		return false
	}
	if l.ch == 'x' {
		sb.WriteByte(byte(v))
	} else {
		if !utf8.ValidRune(rune(v)) {
			return false
		}
		sb.WriteRune(rune(v))
	}
	for range n {
		l.readChar()
	}
	return true
}

func (l *Lexer) readIdentifier() string {
	var sb strings.Builder
	for isIdentifierChar(l.ch) || ((l.ch == '.' || l.ch == '-') && sb.Len() > 0) {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	return sb.String()
}

func (l *Lexer) readNumberWithDecimal() string {
	var sb strings.Builder
	for isDigit(l.ch) {
		sb.WriteByte(l.ch)
		l.readChar()
	}
	if l.ch == '.' && isDigit(l.peekChar()) {
		sb.WriteByte(l.ch)
		l.readChar()
		for isDigit(l.ch) {
			sb.WriteByte(l.ch)
			l.readChar()
		}
	}
	return sb.String()
}

func (l *Lexer) readRegex() string {
	var sb strings.Builder
	l.readChar()

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
		l.readChar()
	}

	return sb.String()
}

// NextToken returns the next token in the input
func (l *Lexer) NextToken() Token {
	var tok Token

	l.skipWhitespace()

	tok.Line = l.line
	tok.Column = l.column

	switch l.ch {
	case 0:
		tok.Type = TokenEOF
		tok.Literal = ""

	case '\n':
		tok.Type = TokenNewline
		tok.Literal = "\n"
		l.readChar()

	case '#':
		l.skipComment()
		return l.NextToken()

	case '+':
		tok.Type = TokenPlus
		tok.Literal = "+"
		l.readChar()

	case '@':
		tok.Type = TokenAt
		tok.Literal = "@"
		l.readChar()

	case ',':
		tok.Type = TokenComma
		tok.Literal = ","
		l.readChar()

	case '/':
		if l.peekChar() == '/' || isIdentifierChar(l.peekChar()) {
			regex := l.readRegex()
			tok.Type = TokenSlash
			tok.Literal = regex
		} else {
			tok.Type = TokenSlash
			tok.Literal = "/"
			l.readChar()
		}

	case '(':
		tok.Type = TokenLParen
		tok.Literal = "("
		l.readChar()

	case ')':
		tok.Type = TokenRParen
		tok.Literal = ")"
		l.readChar()

	case '"', '\'', '`':
		quote := l.ch
		literal := l.readString(quote)
		tok.Type = TokenString
		tok.Literal = literal

	default:
		if isDigit(l.ch) {
			num := l.readNumberWithDecimal()

			if unicode.IsLetter(rune(l.ch)) {
				var sb strings.Builder
				sb.WriteString(num)
				for unicode.IsLetter(rune(l.ch)) {
					for unicode.IsLetter(rune(l.ch)) {
						sb.WriteByte(l.ch)
						l.readChar()
					}
					if isDigit(l.ch) {
						sb.WriteString(l.readNumberWithDecimal())
					}
				}
				tok.Type = TokenDuration
				tok.Literal = sb.String()
			} else {
				tok.Type = TokenNumber
				tok.Literal = num
			}
		} else if isIdentifierChar(l.ch) {
			literal := l.readIdentifier()
			tok.Type = LookupKeyword(literal)
			tok.Literal = literal
		} else {
			tok.Type = TokenIllegal
			tok.Literal = string([]byte{l.ch})
			l.readChar()
		}
	}

	return tok
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isIdentifierChar(ch byte) bool {
	return unicode.IsLetter(rune(ch)) || isDigit(ch) || ch == '_'
}
