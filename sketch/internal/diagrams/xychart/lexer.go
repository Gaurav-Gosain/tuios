// Ported from pele src/diagrams/xychart/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// As in the other lexers, the port reads UTF-8 bytes: code() decodes the character at a
// byte offset, and a step past a character that may not be ASCII steps the whole
// character. pele's closures are methods of a lexer here.

package xychart

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/util"
)

type Tokens struct {
	types  []int
	texts  []string
	starts []int
}

// Lexer states.
const (
	S_INITIAL = iota
	S_axis_data
	S_axis_band_data
	S_data
	S_data_inner
	S_string
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
)

// AT_END is the token each state gives for the empty rest of the input.
var AT_END = [...]int{T_EOF, T_EOF, T_EOF, T_EOF, T_EOF, T_STR, T_acc_title_value, T_acc_descr_value, T_acc_descr_multiline_value}

// BROKEN_RULE: Mermaid's grammar means to open a markdown string at `"` followed by a backtick, but its quoting turns
// the rule into one that matches this text and leaves the current state. Markdown strings are never read.
const BROKEN_RULE = "`)" +
	"                                    " +
	"{ this.pushstate(md_string); }\n<md_string>(?:(?!`\").)+" +
	"                  " +
	"{ return md_str; }\n<md_string>(?:`"

// SINGLE is the token of each one-character punctuation mark.
var SINGLE = func() (out [128]uint8) {
	for _, pair := range [...]struct {
		char byte
		typ  int
	}{
		{':', T_COLON},
		{'+', T_PLUS},
		{',', T_COMMA},
		{'=', T_EQUALS},
		{'*', T_MULT},
		{'#', T_BRKT},
		{'_', T_UNDERSCORE},
		{'.', T_DOT},
		{'&', T_AMP},
		{'-', T_MINUS},
		{';', T_SEMI},
	} {
		out[pair.char] = uint8(pair.typ)
	}
	return
}()

// codeAt decodes the character at a byte offset: -1 past the end.
func codeAt(src string, q int) rune {
	if q < 0 || q >= len(src) {
		return -1
	}
	b := src[q]
	if b < utf8.RuneSelf {
		return rune(b)
	}
	r, _ := utf8.DecodeRuneInString(src[q:])
	return r
}

// sizeAt is the length in bytes of the character at q.
func sizeAt(src string, q int) int {
	if q >= len(src) || src[q] < utf8.RuneSelf {
		return 1
	}
	_, s := utf8.DecodeRuneInString(src[q:])
	return s
}

type lexer struct {
	src   string
	n     int
	toks  Tokens
	stack []int
	state int
	p     int
}

func (l *lexer) code(q int) rune { return codeAt(l.src, q) }

//go:noinline
func (l *lexer) emit(typ int, s, e int) {
	l.toks.types = append(l.toks.types, typ)
	l.toks.texts = append(l.toks.texts, l.src[s:e])
	l.toks.starts = append(l.toks.starts, s)
	l.p = e
}

func (l *lexer) push(s int) {
	l.stack = append(l.stack, l.state)
	l.state = s
}

func (l *lexer) pop() {
	if len(l.stack) > 0 {
		l.state = l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
	}
}

func (l *lexer) lineEnd(q int) int {
	if e := strings.IndexByte(l.src[q:], '\n'); e != -1 {
		return q + e
	}
	return l.n
}

func (l *lexer) until(ch byte, q int) int {
	if e := strings.IndexByte(l.src[q:], ch); e != -1 {
		return q + e
	}
	return l.n
}

func (l *lexer) run(q int) int {
	for q < l.n && util.IsSpace(l.code(q)) {
		q += sizeAt(l.src, q)
	}
	return q
}

func (l *lexer) digits(q int) int {
	for util.IsDigit(l.code(q)) {
		q++
	}
	return q
}

func (l *lexer) keyword(word string) bool {
	return common.WordAt(l.src, l.p, word) && !util.IsWord(l.code(l.p+len(word)))
}

func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src)}
	n := l.n

	for l.p < n {
		p := l.p
		c := l.code(p)

		if l.state == S_string {
			if c == 34 {
				l.p++
				l.pop()
			} else {
				l.emit(T_STR, p, l.until('"', p))
			}
			continue
		}
		if l.state == S_acc_title || l.state == S_acc_descr {
			l.emit(AT_END[l.state], p, l.lineEnd(p))
			l.pop()
			continue
		}
		if l.state == S_acc_descr_multiline {
			if c == 125 {
				l.p++
				l.pop()
			} else {
				l.emit(T_acc_descr_multiline_value, p, l.until('}', p))
			}
			continue
		}

		// The UTF-16 unit after c: a low surrogate after a character outside the BMP.
		size := sizeAt(src, p)
		next := l.code(p + size)
		if size == 4 {
			next = 0xdc00
		}
		if c == 37 && next == 37 {
			l.p = l.lineEnd(p + 2)
			continue
		}
		if c == 10 || c == 13 {
			// Only a line break that directly follows an axis or a series ends it; one after spaces is skipped
			// with them, and the state carries on to the next line.
			if (l.state == S_axis_data || l.state == S_data) && (c == 10 || next == 10) {
				if c == 10 {
					l.emit(T_NEWLINE, p, p+1)
				} else {
					l.emit(T_NEWLINE, p, p+2)
				}
				l.pop()
				continue
			}
			e := p + 1
			for d := l.code(e); d == 10 || d == 13; d = l.code(e) {
				e++
			}
			l.emit(T_NEWLINE, p, e)
			continue
		}

		switch c | 32 {
		case 116:
			if l.keyword("title") {
				l.emit(T_title, p, p+5)
				continue
			}
		case 97:
			if common.WordAt(src, p, "acc") {
				descr := common.WordAt(src, p+3, "descr")
				if descr || common.WordAt(src, p+3, "title") {
					k := l.run(p + 8)
					d := l.code(k)
					if d == 58 {
						if descr {
							l.emit(T_acc_descr, p, l.run(k+1))
							l.push(S_acc_descr)
						} else {
							l.emit(T_acc_title, p, l.run(k+1))
							l.push(S_acc_title)
						}
						continue
					}
					if d == 123 && descr {
						l.p = l.run(k + 1)
						l.push(S_acc_descr_multiline)
						continue
					}
				}
			}
		case 120:
			if l.keyword("xychart-beta") {
				l.emit(T_XYCHART, p, p+12)
				continue
			}
			if l.keyword("xychart") {
				l.emit(T_XYCHART, p, p+7)
				continue
			}
			if l.keyword("x-axis") {
				l.emit(T_X_AXIS, p, p+6)
				l.push(S_axis_data)
				continue
			}
		case 121:
			if l.keyword("y-axis") {
				l.emit(T_Y_AXIS, p, p+6)
				l.push(S_axis_data)
				continue
			}
		case 118:
			if common.WordAt(src, p, "vertical") {
				l.emit(T_CHART_ORIENTATION, p, p+8)
				continue
			}
		case 104:
			if common.WordAt(src, p, "horizontal") {
				l.emit(T_CHART_ORIENTATION, p, p+10)
				continue
			}
		case 108:
			if l.keyword("line") {
				l.emit(T_LINE, p, p+4)
				l.push(S_data)
				continue
			}
		case 98:
			if l.keyword("bar") {
				l.emit(T_BAR, p, p+3)
				l.push(S_data)
				continue
			}
		}

		if c == 91 {
			if l.state == S_axis_data {
				l.push(S_axis_band_data)
			} else if l.state == S_data {
				l.push(S_data_inner)
			}
			l.emit(T_SQUARE_BRACES_START, p, p+1)
			continue
		}
		if c == 93 {
			if l.state == S_data_inner || l.state == S_axis_band_data {
				l.pop()
			}
			l.emit(T_SQUARE_BRACES_END, p, p+1)
			continue
		}
		if l.state == S_axis_data && c == 45 && next == 45 && l.code(p+2) == 62 {
			l.emit(T_ARROW_DELIMITER, p, p+3)
			continue
		}
		if l.state == S_axis_data || l.state == S_data_inner {
			q := p
			if c == 43 || c == 45 {
				q = p + 1
			}
			d := l.code(q)
			e := -1
			if util.IsDigit(d) {
				e = l.digits(q + 1)
				if l.code(e) == 46 && util.IsDigit(l.code(e+1)) {
					e = l.digits(e + 2)
				}
			} else if d == 46 && util.IsDigit(l.code(q+1)) {
				e = l.digits(q + 2)
			}
			if e != -1 {
				l.emit(T_NUMBER_WITH_DECIMAL, p, e)
				continue
			}
		}
		if c == 96 && common.WordAt(src, p, BROKEN_RULE) {
			l.p += len(BROKEN_RULE)
			l.pop()
			continue
		}
		if c == 34 {
			l.p++
			l.push(S_string)
			continue
		}
		if util.IsLetter(c) {
			e := p + 1
			for util.IsLetter(l.code(e)) {
				e++
			}
			l.emit(T_ALPHA, p, e)
			continue
		}
		if util.IsDigit(c) {
			l.emit(T_NUM, p, l.digits(p+1))
			continue
		}
		if util.IsSpace(c) {
			l.p = l.run(p + size)
			continue
		}
		if c < 128 && SINGLE[c] != 0 {
			l.emit(int(SINGLE[c]), p, p+1)
			continue
		}
		break
	}

	t := &l.toks
	if l.p < n {
		t.types = append(t.types, T_ERROR)
		t.texts = append(t.texts, "")
		t.starts = append(t.starts, l.p)
		return *t
	}
	// Jison tries the rules of the current state once more against the empty rest of the input.
	t.types = append(t.types, AT_END[l.state], T_END)
	t.texts = append(t.texts, "", "")
	t.starts = append(t.starts, n, n)
	return *t
}
