package tml

import (
	"fmt"
	"unicode/utf8"
)

// jsonScanner checks JSON syntax and reports the first error with tml's
// own message and byte offset. encoding/json words its errors and places
// its offsets differently from one Go release to the next, so a problem
// built from them would change with the toolchain.
type jsonScanner struct {
	src   []byte
	pos   int
	depth int
}

// jsonError is a syntax error at a byte offset.
type jsonError struct {
	off int
	msg string
}

func (e *jsonError) Error() string { return e.msg }

// maxJSONDepth matches the nesting encoding/json accepts.
const maxJSONDepth = 10000

// scanJSON checks that src holds one JSON value, with only white space
// around it.
func scanJSON(src []byte) *jsonError {
	s := &jsonScanner{src: src}
	s.space()
	if err := s.value(); err != nil {
		return err
	}
	s.space()
	if s.pos < len(s.src) {
		return s.fail("Text after the end of the JSON value. A document has one root object.")
	}
	return nil
}

func (s *jsonScanner) fail(format string, args ...any) *jsonError {
	return &jsonError{off: s.pos, msg: fmt.Sprintf(format, args...)}
}

// found describes the byte at the scan position for a message.
func (s *jsonScanner) found() string {
	if s.pos >= len(s.src) {
		return "the end of the text"
	}
	r, _ := utf8.DecodeRune(s.src[s.pos:])
	if r >= 0x20 && r < 0x7f {
		return fmt.Sprintf("%q", r)
	}
	return fmt.Sprintf("the character U+%04X", r)
}

func (s *jsonScanner) space() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *jsonScanner) value() *jsonError {
	if s.pos >= len(s.src) {
		return s.fail("The JSON ends too early. Check for a missing } or ].")
	}
	switch c := s.src[s.pos]; {
	case c == '{':
		return s.object()
	case c == '[':
		return s.array()
	case c == '"':
		return s.str()
	case c == '-' || c >= '0' && c <= '9':
		return s.number()
	case c == 't':
		return s.word("true")
	case c == 'f':
		return s.word("false")
	case c == 'n':
		return s.word("null")
	}
	return s.fail("Expected a JSON value here, found %s.", s.found())
}

func (s *jsonScanner) enter() *jsonError {
	s.depth++
	if s.depth > maxJSONDepth {
		return s.fail("The JSON nests deeper than %d levels.", maxJSONDepth)
	}
	return nil
}

func (s *jsonScanner) object() *jsonError {
	if err := s.enter(); err != nil {
		return err
	}
	s.pos++ // {
	s.space()
	if s.pos < len(s.src) && s.src[s.pos] == '}' {
		s.pos++
		s.depth--
		return nil
	}
	for {
		s.space()
		if s.pos >= len(s.src) || s.src[s.pos] != '"' {
			if s.pos >= len(s.src) {
				return s.fail("The JSON ends too early. Check for a missing } or ].")
			}
			return s.fail("Expected a key in quotes here, found %s.", s.found())
		}
		if err := s.str(); err != nil {
			return err
		}
		s.space()
		if s.pos >= len(s.src) || s.src[s.pos] != ':' {
			return s.fail("Expected : after the key, found %s.", s.found())
		}
		s.pos++
		s.space()
		if err := s.value(); err != nil {
			return err
		}
		s.space()
		if s.pos < len(s.src) && s.src[s.pos] == ',' {
			s.pos++
			continue
		}
		if s.pos < len(s.src) && s.src[s.pos] == '}' {
			s.pos++
			s.depth--
			return nil
		}
		return s.fail("Expected , or } here, found %s.", s.found())
	}
}

func (s *jsonScanner) array() *jsonError {
	if err := s.enter(); err != nil {
		return err
	}
	s.pos++ // [
	s.space()
	if s.pos < len(s.src) && s.src[s.pos] == ']' {
		s.pos++
		s.depth--
		return nil
	}
	for {
		s.space()
		if err := s.value(); err != nil {
			return err
		}
		s.space()
		if s.pos < len(s.src) && s.src[s.pos] == ',' {
			s.pos++
			continue
		}
		if s.pos < len(s.src) && s.src[s.pos] == ']' {
			s.pos++
			s.depth--
			return nil
		}
		return s.fail("Expected , or ] here, found %s.", s.found())
	}
}

func (s *jsonScanner) str() *jsonError {
	s.pos++ // "
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		switch {
		case c == '"':
			s.pos++
			return nil
		case c == '\\':
			if s.pos+1 >= len(s.src) {
				s.pos++
				return s.fail("The JSON ends inside a string.")
			}
			switch s.src[s.pos+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.pos += 2
			case 'u':
				for i := 2; i < 6; i++ {
					if s.pos+i >= len(s.src) || !isHex(s.src[s.pos+i]) {
						return s.fail("A \\u escape needs four hex digits.")
					}
				}
				s.pos += 6
			default:
				return s.fail("Unknown escape in a string. Use \\\" \\\\ \\/ \\b \\f \\n \\r \\t or \\u.")
			}
		case c < 0x20:
			return s.fail("A control character inside a string. Write it as an escape, such as \\n.")
		default:
			s.pos++
		}
	}
	return s.fail("The JSON ends inside a string.")
}

func (s *jsonScanner) number() *jsonError {
	if s.src[s.pos] == '-' {
		s.pos++
	}
	digits := func() int {
		n := 0
		for s.pos < len(s.src) && isDigit(s.src[s.pos]) {
			s.pos++
			n++
		}
		return n
	}
	switch {
	case s.pos < len(s.src) && s.src[s.pos] == '0':
		s.pos++
	case digits() == 0:
		return s.fail("Expected a digit here, found %s.", s.found())
	}
	if s.pos < len(s.src) && s.src[s.pos] == '.' {
		s.pos++
		if digits() == 0 {
			return s.fail("Expected a digit after the decimal point, found %s.", s.found())
		}
	}
	if s.pos < len(s.src) && (s.src[s.pos] == 'e' || s.src[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.src) && (s.src[s.pos] == '+' || s.src[s.pos] == '-') {
			s.pos++
		}
		if digits() == 0 {
			return s.fail("Expected a digit in the exponent, found %s.", s.found())
		}
	}
	return nil
}

func (s *jsonScanner) word(w string) *jsonError {
	for i := 0; i < len(w); i++ {
		if s.pos >= len(s.src) || s.src[s.pos] != w[i] {
			return s.fail("Expected %s here, found %s.", w, s.found())
		}
		s.pos++
	}
	return nil
}
