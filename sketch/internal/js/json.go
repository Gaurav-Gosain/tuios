package js

import (
	"slices"
	"strconv"
	"unicode/utf16"
)

// JSONObject is an object JSON.parse made. Keys are its own properties in the order
// Object.keys gives them: integer keys first, from the smallest, then the others in the
// order they were first written. A key written twice keeps its first place and its last
// value.
type JSONObject struct {
	Keys   []string
	Values map[string]any
}

// JSON tokens, as V8's JsonParser names the token of a character.
const (
	jsonEOS = iota
	jsonString
	jsonNumber
	jsonLiteral
	jsonWhitespace
	jsonColon
	jsonComma
	jsonLBrack
	jsonRBrack
	jsonLBrace
	jsonRBrace
	jsonIllegal
)

func jsonToken(c int) int {
	switch {
	case c < 0:
		return jsonEOS
	case c == '"':
		return jsonString
	case c == '-' || (c >= '0' && c <= '9'):
		return jsonNumber
	case c == 't' || c == 'f' || c == 'n':
		return jsonLiteral
	case c == ' ' || c == '\t' || c == '\r' || c == '\n':
		return jsonWhitespace
	case c == ':':
		return jsonColon
	case c == ',':
		return jsonComma
	case c == '[':
		return jsonLBrack
	case c == ']':
		return jsonRBrack
	case c == '{':
		return jsonLBrace
	case c == '}':
		return jsonRBrace
	}
	return jsonIllegal
}

// JSONError is the SyntaxError JSON.parse throws, with V8's message.
type JSONError struct{ Message string }

func (e *JSONError) Error() string { return e.Message }

type jsonParser struct {
	s   []uint16
	pos int
}

func (p *jsonParser) cur() int {
	if p.pos >= len(p.s) {
		return -1
	}
	return int(p.s[p.pos])
}

func (p *jsonParser) skip() {
	for {
		c := p.cur()
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return
		}
		p.pos++
	}
}

// location is V8's CalculateFileLocation: the line and column of the cursor, where \r\n
// is one line break.
func (p *jsonParser) location() (int, int) {
	line := 1
	lastBreak := 0
	k := 0
	for ; k < p.pos; k++ {
		if p.s[k] == '\r' && k < p.pos-1 && p.s[k+1] == '\n' {
			k++
		}
		if p.s[k] == '\r' || p.s[k] == '\n' {
			line++
			lastBreak = k + 1
		}
	}
	return line, 1 + k - lastBreak
}

// fail is V8's ReportUnexpectedToken: the message given, or the one for the token at the cursor.
func (p *jsonParser) fail(message string) {
	if message == "" {
		switch jsonToken(p.cur()) {
		case jsonEOS:
			panic(&JSONError{"Unexpected end of JSON input"})
		case jsonNumber:
			message = "Unexpected number"
		case jsonString:
			message = "Unexpected string"
		default:
			ch := string(utf16.Decode(p.s[p.pos : p.pos+1]))
			n := len(p.s)
			const context = 10
			var text string
			switch {
			case n < 2*context+1:
				text = `"` + DecodeUTF16(p.s) + `"`
			case p.pos < context:
				text = `"` + DecodeUTF16(p.s[:p.pos+context]) + `"...`
			case p.pos < n-context:
				text = `..."` + DecodeUTF16(p.s[p.pos-context:p.pos+context]) + `"...`
			default:
				text = `..."` + DecodeUTF16(p.s[p.pos-context:]) + `"`
			}
			panic(&JSONError{"Unexpected token '" + ch + "', " + text + " is not valid JSON"})
		}
	}
	line, column := p.location()
	if message != "Unexpected non-whitespace character after JSON" {
		message += " in JSON"
	}
	panic(&JSONError{message + " at position " + strconv.Itoa(p.pos) + " (line " + strconv.Itoa(line) + " column " + strconv.Itoa(column) + ")"})
}

// expect is V8's ExpectNext: whitespace, then the token, or the message.
func (p *jsonParser) expect(token int, message string) {
	p.skip()
	if jsonToken(p.cur()) != token {
		p.fail(message)
	}
	p.pos++
}

// check is V8's Check: whitespace, then the token if it is there.
func (p *jsonParser) check(token int) bool {
	p.skip()
	if jsonToken(p.cur()) != token {
		return false
	}
	p.pos++
	return true
}

func hexValue(c int) int {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

// str reads a string whose opening quote is at the cursor.
func (p *jsonParser) str() string {
	p.pos++
	var out []uint16
	for {
		c := p.cur()
		switch {
		case c < 0:
			p.fail("Unterminated string")
		case c == '"':
			p.pos++
			return DecodeUTF16(out)
		case c < 0x20:
			p.fail("Bad control character in string literal")
		case c == '\\':
			p.pos++
			e := p.cur()
			switch e {
			case '"', '\\', '/':
				out = append(out, uint16(e))
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				value := 0
				for range 4 {
					p.pos++
					digit := hexValue(p.cur())
					if digit < 0 {
						p.fail("Bad Unicode escape")
					}
					value = value*16 + digit
				}
				out = append(out, uint16(value))
			default:
				if e < 0 || e > 0xff {
					// A character past Latin-1 is no escape V8 knows, and is reported as itself.
					p.fail("")
				}
				p.fail("Bad escaped character")
			}
			p.pos++
		default:
			out = append(out, uint16(c))
			p.pos++
		}
	}
}

func isDecimal(c int) bool { return c >= '0' && c <= '9' }

// number reads a number that starts at the cursor.
func (p *jsonParser) number() float64 {
	start := p.pos
	if p.cur() == '-' {
		p.pos++
	}
	if p.cur() == '0' {
		p.pos++
		if isDecimal(p.cur()) {
			p.fail("")
		}
	} else {
		if !isDecimal(p.cur()) {
			p.fail("No number after minus sign")
		}
		for isDecimal(p.cur()) {
			p.pos++
		}
	}
	if p.cur() == '.' {
		p.pos++
		if !isDecimal(p.cur()) {
			p.fail("Unterminated fractional number")
		}
		for isDecimal(p.cur()) {
			p.pos++
		}
	}
	if p.cur()|32 == 'e' {
		p.pos++
		if p.cur() == '-' || p.cur() == '+' {
			p.pos++
		}
		if !isDecimal(p.cur()) {
			p.fail("Exponent part is missing a number")
		}
		for isDecimal(p.cur()) {
			p.pos++
		}
	}
	b := make([]byte, p.pos-start)
	for k := range b {
		b[k] = byte(p.s[start+k])
	}
	v, _ := strconv.ParseFloat(string(b), 64)
	return v
}

// literal reads true, false or null, failing at the first character that differs.
func (p *jsonParser) literal(word string) {
	for k := 0; k < len(word); k++ {
		if p.cur() != int(word[k]) {
			p.fail("")
		}
		p.pos++
	}
}

type jsonFrame struct {
	object *JSONObject
	array  *[]any
	key    string
}

// ParseJSON is JSON.parse of UTF-16 text as V8 does it, with V8's messages for text it
// rejects. Values are nil, bool, float64, string, []any and *JSONObject.
func ParseJSON(s []uint16) (value any, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*JSONError)
			if !ok {
				panic(r)
			}
			value, err = nil, e
		}
	}()
	p := &jsonParser{s: s}
	var stack []jsonFrame
	for {
		// A value.
	readValue:
		for {
			p.skip()
			switch jsonToken(p.cur()) {
			case jsonString:
				value = p.str()
			case jsonNumber:
				value = p.number()
			case jsonLBrace:
				p.pos++
				obj := &JSONObject{Values: map[string]any{}}
				if p.check(jsonRBrace) {
					value = obj
					break readValue
				}
				p.expect(jsonString, "Expected property name or '}'")
				p.pos--
				key := p.str()
				stack = append(stack, jsonFrame{object: obj, key: key})
				p.expect(jsonColon, "Expected ':' after property name")
				continue
			case jsonLBrack:
				p.pos++
				arr := []any{}
				if p.check(jsonRBrack) {
					value = arr
					break readValue
				}
				stack = append(stack, jsonFrame{array: &arr})
				continue
			case jsonLiteral:
				switch p.cur() {
				case 't':
					p.literal("true")
					value = true
				case 'f':
					p.literal("false")
					value = false
				default:
					p.literal("null")
					value = nil
				}
			default:
				p.fail("")
			}
			break
		}
		// What follows it.
	readFollow:
		for {
			if len(stack) == 0 {
				if !p.check(jsonEOS) {
					p.fail("Unexpected non-whitespace character after JSON")
				}
				return value, nil
			}
			top := &stack[len(stack)-1]
			if top.object != nil {
				obj := top.object
				if _, ok := obj.Values[top.key]; !ok {
					obj.Keys = append(obj.Keys, top.key)
				}
				obj.Values[top.key] = value
				if p.check(jsonComma) {
					p.expect(jsonString, "Expected double-quoted property name")
					p.pos--
					top.key = p.str()
					p.expect(jsonColon, "Expected ':' after property name")
					break readFollow
				}
				p.skip()
				if jsonToken(p.cur()) != jsonRBrace {
					p.fail("Expected ',' or '}' after property value")
				}
				p.pos++
				orderKeys(obj)
				value = obj
			} else {
				*top.array = append(*top.array, value)
				if p.check(jsonComma) {
					break readFollow
				}
				p.skip()
				if jsonToken(p.cur()) != jsonRBrack {
					p.fail("Expected ',' or ']' after array element")
				}
				p.pos++
				value = *top.array
			}
			stack = stack[:len(stack)-1]
		}
	}
}

// arrayIndex is the value of a key that is an array index, a canonical number below 2^32-1, or -1.
func arrayIndex(key string) int64 {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return -1
	}
	n := int64(0)
	for k := 0; k < len(key); k++ {
		if key[k] < '0' || key[k] > '9' {
			return -1
		}
		n = n*10 + int64(key[k]-'0')
	}
	if n >= 1<<32-1 {
		return -1
	}
	return n
}

// orderKeys puts integer keys first, in order, as JavaScript orders an object's own keys.
func orderKeys(obj *JSONObject) {
	var ints []string
	rest := obj.Keys[:0:0]
	for _, k := range obj.Keys {
		if arrayIndex(k) >= 0 {
			ints = append(ints, k)
		} else {
			rest = append(rest, k)
		}
	}
	if len(ints) == 0 {
		return
	}
	slices.SortFunc(ints, func(a, b string) int { return int(arrayIndex(a) - arrayIndex(b)) })
	obj.Keys = append(ints, rest...)
}

// UTF16 is a string as JavaScript holds it.
func UTF16(s string) []uint16 { return utf16.Encode([]rune(s)) }

// DecodeUTF16 turns UTF-16 units into a string; a lone surrogate becomes U+FFFD.
func DecodeUTF16(u []uint16) string { return string(utf16.Decode(u)) }
