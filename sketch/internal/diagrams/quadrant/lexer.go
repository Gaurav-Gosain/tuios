// Ported from pele src/diagrams/quadrant/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// As in the other lexers, the port reads UTF-8 bytes: code() decodes the character at a
// byte offset, and a step past a character that may not be ASCII steps the whole
// character. pele's closures are methods of a lexer here.

package quadrant

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
	S_title
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
	S_string
	S_md_string
	S_class_name
	S_point_start
	S_point_x
	S_point_y
)

// AT_END is the token each state gives for the empty rest of the input, where it gives one.
var AT_END = [...]int{T_EOF, T_title_value, T_acc_title_value, T_acc_descr_value, T_acc_descr_multiline_value, T_STR}

// SINGLE is the token of each one-character mark.
var SINGLE = func() (out [128]uint8) {
	for _, pair := range [...]struct {
		chars string
		typ   int
	}{
		{":", T_COLON},
		{"+", T_PLUS},
		{",", T_COMMA},
		{"=", T_EQUALS},
		{"*", T_MULT},
		{"#", T_BRKT},
		{"_", T_UNDERSCORE},
		{".", T_DOT},
		{"&", T_AMP},
		{"-", T_MINUS},
		{" \t\v\f", T_SPACE},
		{";", T_SEMI},
		{"!$%'`?\\/", T_PUNCTUATION},
	} {
		for k := 0; k < len(pair.chars); k++ {
			out[pair.chars[k]] = uint8(pair.typ)
		}
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
	// The last run scanned of each kind, so that a long run is not scanned again from each of its characters.
	spaceFrom, spaceTo int
	dashFrom, dashTo   int
	wsFrom, wsTo       int
	gapFrom, gapTo     int
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

func (l *lexer) until(ch byte, q int) int {
	if e := strings.IndexByte(l.src[q:], ch); e != -1 {
		return q + e
	}
	return l.n
}

func (l *lexer) lineEnd(q int) int { return l.until('\n', q) }

func (l *lexer) spaces(q int) int {
	for l.code(q) == 32 {
		q++
	}
	return q
}

func (l *lexer) run(q int) int {
	for q < l.n && util.IsSpace(l.code(q)) {
		q += sizeAt(l.src, q)
	}
	return q
}

// coordinate is Mermaid's `(1)|(0(.\d+)?)`, where the dot is any character within a line.
func (l *lexer) coordinate(q int) int {
	c := l.code(q)
	if c == 49 {
		return q + 1
	}
	if c != 48 {
		return -1
	}
	e := q + 1
	// The dot is one UTF-16 unit: a character outside the BMP is two, and the second is no digit.
	if size := sizeAt(l.src, e); e < l.n && size < 4 && !util.IsLineEnd(l.code(e)) && util.IsDigit(l.code(e+size)) {
		e += size + 1
		for util.IsDigit(l.code(e)) {
			e++
		}
	}
	return e
}

func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src)}
	n := l.n

lexing:
	for l.p < n {
		p := l.p
		c := l.code(p)
		size := sizeAt(src, p)
		// The UTF-16 unit after c: a low surrogate after a character outside the BMP.
		next := l.code(p + size)
		if size == 4 {
			next = 0xdc00
		}

		switch l.state {
		case S_INITIAL:

		case S_title, S_acc_title, S_acc_descr:
			l.emit(AT_END[l.state], p, l.lineEnd(p))
			l.pop()
			continue

		case S_acc_descr_multiline:
			if c == 125 {
				l.p++
				l.pop()
			} else {
				l.emit(T_acc_descr_multiline_value, p, l.until('}', p))
			}
			continue

		case S_string:
			if c == 34 {
				l.p++
				l.pop()
			} else {
				l.emit(T_STR, p, l.until('"', p))
			}
			continue

		case S_md_string:
			if c == 96 && next == 34 {
				l.p += 2
				l.pop()
				continue
			}
			if c != 96 && c != 34 {
				e := p + size
				for e < n && src[e] != '`' && src[e] != '"' {
					e++
				}
				l.emit(T_MD_STR, p, e)
				continue
			}
			break lexing

		case S_class_name:
			if util.IsWord(c) {
				e := p + 1
				for util.IsWord(l.code(e)) {
					e++
				}
				l.emit(T_class_name, p, e)
				l.pop()
				continue
			}
			break lexing

		case S_point_start:
			if e := l.coordinate(p); e != -1 {
				l.emit(T_point_x, p, e)
				l.push(S_point_x)
				continue
			}
			if k := l.run(p); l.code(k) == 93 {
				l.p = l.spaces(k + 1)
				l.pop()
				continue
			}
			break lexing

		case S_point_x:
			if k := l.run(p); l.code(k) == 44 {
				l.p = l.run(k + 1)
				l.state = S_point_y
				continue
			}
			break lexing

		case S_point_y:
			if e := l.coordinate(p); e != -1 {
				l.emit(T_point_y, p, e)
				l.pop()
				continue
			}
			break lexing
		}

		if c == 37 && next == 37 {
			l.p = l.lineEnd(p + 2)
			continue
		}
		if c == 10 || c == 13 {
			e := p + 1
			for d := l.code(e); d == 10 || d == 13; d = l.code(e) {
				e++
			}
			l.emit(T_NEWLINE, p, e)
			continue
		}

		lower := c | 32
		if lower == 116 && common.WordAt(src, p, "title") && !util.IsWord(l.code(p+5)) {
			l.emit(T_title, p, p+5)
			l.push(S_title)
			continue
		}
		if lower == 97 && common.WordAt(src, p, "acc") {
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

		// Axis, quadrant and chart keywords and the axis arrow take the spaces around them.
		j := p
		if c == 32 {
			if p < l.spaceFrom || p >= l.spaceTo {
				l.spaceFrom = p
				l.spaceTo = l.spaces(p)
			}
			j = l.spaceTo
		}
		d := l.code(j)
		dl := d | 32
		typ := 0
		end := 0
		if dl == 120 && common.WordAt(src, j, "x-axis") {
			typ = T_X_AXIS
			end = j + 6
		} else if dl == 121 && common.WordAt(src, j, "y-axis") {
			typ = T_Y_AXIS
			end = j + 6
		} else if d == 45 {
			if j < l.dashFrom || j >= l.dashTo {
				l.dashFrom = j
				l.dashTo = j
				for l.code(l.dashTo) == 45 {
					l.dashTo++
				}
			}
			if l.dashTo-j >= 2 && l.code(l.dashTo) == 62 {
				typ = T_DELIMITER
				end = l.dashTo + 1
			}
		} else if dl == 113 && common.WordAt(src, j, "quadrant") {
			q := l.code(j + 9)
			if l.code(j+8) == 45 && q >= 49 && q <= 52 {
				typ = T_QUADRANT_1 + int(q) - 49
				end = j + 10
			} else if common.WordAt(src, j+8, "chart") {
				typ = T_QUADRANT
				end = j + 13
			}
		}
		if typ != 0 {
			l.emit(typ, p, l.spaces(end))
			continue
		}

		if lower == 99 && common.WordAt(src, p, "classdef") && !util.IsWord(l.code(p+8)) {
			l.emit(T_CLASSDEF, p, p+8)
			continue
		}
		if c == 34 {
			if next == 96 {
				l.p += 2
				l.push(S_md_string)
			} else {
				l.p++
				l.push(S_string)
			}
			continue
		}
		if c == 58 && next == 58 && l.code(p+2) == 58 {
			l.p += 3
			l.push(S_class_name)
			continue
		}
		// A point's `: [`, with any whitespace before, between and after, line breaks included.
		if c == 58 || util.IsSpace(c) {
			k := p
			if c != 58 {
				if p < l.wsFrom || p >= l.wsTo {
					l.wsFrom = p
					l.wsTo = l.run(p)
				}
				k = l.wsTo
			}
			if l.code(k) == 58 {
				if k+1 < l.gapFrom || k+1 >= l.gapTo {
					l.gapFrom = k + 1
					l.gapTo = l.run(k + 1)
				}
				if l.code(l.gapTo) == 91 {
					l.emit(T_point_start, p, l.run(l.gapTo+1))
					l.push(S_point_start)
					continue
				}
			}
		}

		if util.IsLetter(c) {
			e := p + 1
			for util.IsLetter(l.code(e)) {
				e++
			}
			l.emit(T_ALPHA, p, e)
			continue
		}
		if c >= 128 {
			e := p + 1
			for e < n && src[e] >= utf8.RuneSelf {
				e++
			}
			l.emit(T_UNICODE_TEXT, p, e)
			continue
		}
		if util.IsDigit(c) {
			e := p + 1
			for util.IsDigit(l.code(e)) {
				e++
			}
			l.emit(T_NUM, p, e)
			continue
		}
		if SINGLE[c] != 0 {
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
	if l.state < len(AT_END) {
		t.types = append(t.types, AT_END[l.state])
		t.texts = append(t.texts, "")
		t.starts = append(t.starts, n)
	}
	t.types = append(t.types, T_END)
	t.texts = append(t.texts, "")
	t.starts = append(t.starts, n)
	return *t
}
