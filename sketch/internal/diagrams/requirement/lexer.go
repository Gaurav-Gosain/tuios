// Ported from pele src/diagrams/requirement/lexer.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// The port reads UTF-8 bytes, and offsets are byte offsets. Every character the lexer
// matches is ASCII, except whitespace, which is read as a whole character. The regular
// expressions are hand matchers: with the i flag and without u, only ASCII letters fold.

package requirement

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/util"
)

type Tokens struct {
	types  []int
	texts  []string
	starts []int
}

const (
	S_INITIAL = iota
	S_string
	S_style
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
)

const LONGEST_KEYWORD = 22

// isStop: characters that end an unquoted string: `:,{<>-=` and line breaks.
func isStop(c byte) bool {
	return strings.IndexByte(":,\r\n{<>-=", c) >= 0
}

func byteAt(src string, q int) int {
	if q < 0 || q >= len(src) {
		return -1
	}
	return int(src[q])
}

// spaceAt is the length in bytes of the whitespace character at q, or 0 if there is none.
func spaceAt(src string, q int) int {
	if q >= len(src) {
		return 0
	}
	if c := src[q]; c < utf8.RuneSelf {
		if util.IsSpace(rune(c)) {
			return 1
		}
		return 0
	}
	r, size := utf8.DecodeRuneInString(src[q:])
	if util.IsSpace(r) {
		return size
	}
	return 0
}

func spaces(src string, q int) int {
	for {
		s := spaceAt(src, q)
		if s == 0 {
			return q
		}
		q += s
	}
}

// hasFold reports whether src at q starts with word, ASCII letters in any case. word is lower case.
func hasFold(src string, q int, word string) bool {
	return q+len(word) <= len(src) && strings.EqualFold(src[q:q+len(word)], word) && isASCII(src[q:q+len(word)])
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// matchAcc is /accTitle\s*:\s*/iy, /accDescr\s*:\s*/iy or /accDescr\s*\{\s*/iy at q: the end, or -1.
func matchAcc(src string, q int, word string, sep byte) int {
	if !hasFold(src, q, word) {
		return -1
	}
	e := spaces(src, q+len(word))
	if byteAt(src, e) != int(sep) {
		return -1
	}
	return spaces(src, e+1)
}

// directions finds every match of /direction\s+(?:(TB)|(BT)|(RL)|LR)/gi, as exec does with the g flag.
func directions(src string) (starts, ends [4][]int, any bool) {
	for p := 0; p+len("direction") <= len(src); {
		at := indexFold(src, p, "direction")
		if at < 0 {
			break
		}
		q := at + len("direction")
		e := spaces(src, q)
		kind := -1
		if e > q && e+2 <= len(src) {
			switch strings.ToLower(src[e : e+2]) {
			case "tb":
				kind = 0
			case "bt":
				kind = 1
			case "rl":
				kind = 2
			case "lr":
				kind = 3
			}
			if kind >= 0 && !isASCII(src[e:e+2]) {
				kind = -1
			}
		}
		if kind < 0 {
			p = at + 1
			continue
		}
		starts[kind] = append(starts[kind], at)
		ends[kind] = append(ends[kind], e+2)
		any = true
		p = e + 2
	}
	return starts, ends, any
}

// indexFold is the first place at or after p where word starts, ASCII letters in any case, or -1.
func indexFold(src string, p int, word string) int {
	for q := p; q+len(word) <= len(src); q++ {
		if src[q]|32 == word[0] && hasFold(src, q, word) {
			return q
		}
	}
	return -1
}

func isWordAt(src string, q int) bool {
	return q < len(src) && util.IsWord(rune(src[q]))
}

// tokenize produces the token stream of Mermaid's requirement lexer. Its rules are tried in order and
// the first match wins, so a line that holds a direction statement anywhere is one token.
func tokenize(src string) Tokens {
	n := len(src)
	var out Tokens
	var stack []int
	state := S_INITIAL
	p := 0

	// Where each kind of direction statement starts and where its keyword ends, found in one pass.
	dirStarts, dirEnds, anyDir := directions(src)
	var dirSeen [4]int
	lineEnd := -1

	emit := func(typ, s, e int) {
		out.types = append(out.types, typ)
		out.texts = append(out.texts, src[s:e])
		out.starts = append(out.starts, s)
		p = e
	}
	push := func(s int) {
		stack = append(stack, state)
		state = s
	}
	pop := func() {
		if len(stack) > 0 {
			state = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		}
	}
	lineBreak := func(q int) int {
		if e := strings.IndexByte(src[q:], '\n'); e >= 0 {
			return q + e
		}
		return n
	}

	// direction: `.*direction\s+XX[^\n]*`: the last such statement that starts on this line decides the token.
	direction := func() bool {
		if lineEnd < p {
			lineEnd = n
			for q := p; q < n; q++ {
				c := src[q]
				if c == '\n' || c == '\r' {
					lineEnd = q
					break
				}
				if c == 0xe2 && q+2 < n && src[q+1] == 0x80 && (src[q+2] == 0xa8 || src[q+2] == 0xa9) {
					lineEnd = q
					break
				}
			}
			for k := 0; k < 4; k++ {
				list := dirStarts[k]
				j := dirSeen[k]
				for j < len(list) && list[j] <= lineEnd {
					j++
				}
				dirSeen[k] = j
			}
		}
		for k := 0; k < 4; k++ {
			j := dirSeen[k] - 1
			if j >= 0 && dirStarts[k][j] >= p {
				emit(T_direction_tb+k, p, lineBreak(dirEnds[k][j]))
				return true
			}
		}
		return false
	}

	isLineEndAt := func(q int) bool {
		c := src[q]
		if c < utf8.RuneSelf {
			return util.IsLineEnd(rune(c))
		}
		r, _ := utf8.DecodeRuneInString(src[q:])
		return util.IsLineEnd(r)
	}

	initial := func(c int) bool {
		lower := c | 32
		if lower == 116 && p+5 <= n && strings.ToLower(src[p+1:p+5]) == "itle" && isASCII(src[p+1:p+5]) {
			if s := spaceAt(src, p+5); s > 0 {
				from := p + 5 + s
				q := from
				for q < n {
					d := src[q]
					if d == 35 || d == 10 || d == 59 {
						break
					}
					q++
				}
				if q > from {
					emit(T_title, p, q)
					return true
				}
			}
		}
		if lower == 97 && byteAt(src, p+1)|32 == 99 && byteAt(src, p+2)|32 == 99 {
			if e := matchAcc(src, p, "acctitle", ':'); e >= 0 {
				push(S_acc_title)
				emit(T_acc_title, p, e)
				return true
			}
			if e := matchAcc(src, p, "accdescr", ':'); e >= 0 {
				push(S_acc_descr)
				emit(T_acc_descr, p, e)
				return true
			}
			if e := matchAcc(src, p, "accdescr", '{'); e >= 0 {
				push(S_acc_descr_multiline)
				p = e
				return true
			}
		}
		if anyDir && !isLineEndAt(p) && direction() {
			return true
		}

		if c == 10 || (c == 13 && byteAt(src, p+1) == 10) {
			q := p
			for q < n {
				d := src[q]
				if d == 10 {
					q++
				} else if d == 13 && byteAt(src, q+1) == 10 {
					q += 2
				} else {
					break
				}
			}
			emit(T_NEWLINE, p, q)
			return true
		}
		if s := spaceAt(src, p); s > 0 {
			p = spaces(src, p+s)
			return true
		}
		if c == 35 || c == 37 {
			p = lineBreak(p)
			return true
		}

		if c < utf8.RuneSelf && util.IsWord(rune(c)) {
			q := p + 1
			for isWordAt(src, q) {
				q++
			}
			if q-p <= LONGEST_KEYWORD {
				if kw, ok := keyword(strings.ToLower(src[p:q])); ok {
					if kw == T_STYLE || kw == T_CLASSDEF || kw == T_CLASS {
						push(S_style)
					}
					emit(kw, p, q)
					return true
				}
			}
			for q < n {
				d := src[q]
				if d < utf8.RuneSelf && isStop(d) {
					break
				}
				q++
			}
			out.types = append(out.types, T_unqString)
			out.texts = append(out.texts, js.Trim(src[p:q]))
			out.starts = append(out.starts, p)
			p = q
			return true
		}

		switch c {
		case 123:
			emit(T_STRUCT_START, p, p+1)
			return true
		case 125:
			emit(T_STRUCT_STOP, p, p+1)
			return true
		case 58:
			if byteAt(src, p+1) == 58 && byteAt(src, p+2) == 58 {
				emit(T_STYLE_SEPARATOR, p, p+3)
			} else {
				emit(T_COLONSEP, p, p+1)
			}
			return true
		case 60:
			if byteAt(src, p+1) != 45 {
				return false
			}
			emit(T_END_ARROW_L, p, p+2)
			return true
		case 45:
			if byteAt(src, p+1) == 62 {
				emit(T_END_ARROW_R, p, p+2)
			} else {
				emit(T_LINE, p, p+1)
			}
			return true
		case 34:
			push(S_string)
			p++
			return true
		case 44:
			emit(T_COMMA, p, p+1)
			return true
		}
		return false
	}

	style := func(c int) bool {
		if c < utf8.RuneSelf && util.IsWord(rune(c)) {
			q := p + 1
			for isWordAt(src, q) {
				q++
			}
			emit(T_ALPHA, p, q)
			return true
		}
		switch c {
		case 58:
			emit(T_COLON, p, p+1)
			return true
		case 59:
			emit(T_SEMICOLON, p, p+1)
			return true
		case 37:
			emit(T_PERCENT, p, p+1)
			return true
		case 45:
			emit(T_MINUS, p, p+1)
			return true
		case 35:
			emit(T_BRKT, p, p+1)
			return true
		case 32:
			p++
			return true
		case 34:
			push(S_string)
			p++
			return true
		case 10:
			pop()
			p++
			return true
		case 44:
			emit(T_COMMA, p, p+1)
			return true
		}
		return false
	}

	for p < n {
		c := int(src[p])
		ok := true
		switch state {
		case S_INITIAL:
			ok = initial(c)
		case S_style:
			ok = style(c)
		case S_string:
			if c == 34 {
				pop()
				p++
			} else if e := strings.IndexByte(src[p:], '"'); e >= 0 {
				emit(T_qString, p, p+e)
			} else {
				emit(T_qString, p, n)
			}
		case S_acc_title:
			pop()
			emit(T_acc_title_value, p, lineBreak(p))
		case S_acc_descr:
			pop()
			emit(T_acc_descr_value, p, lineBreak(p))
		case S_acc_descr_multiline:
			if c == 125 {
				pop()
				p++
			} else if e := strings.IndexByte(src[p:], '}'); e >= 0 {
				emit(T_acc_descr_multiline_value, p, p+e)
			} else {
				emit(T_acc_descr_multiline_value, p, n)
			}
		}
		if !ok {
			out.types = append(out.types, T_ERROR)
			out.texts = append(out.texts, "")
			out.starts = append(out.starts, p)
			return out
		}
	}

	// At the end of input Jison still tries the rules that can match nothing, once.
	last := -1
	switch state {
	case S_INITIAL:
		last = T_EOF
	case S_string:
		last = T_qString
	case S_acc_title:
		last = T_acc_title_value
	case S_acc_descr:
		last = T_acc_descr_value
	case S_acc_descr_multiline:
		last = T_acc_descr_multiline_value
	}
	if last != -1 {
		emit(last, n, n)
	}
	emit(T_END, n, n)
	return out
}
