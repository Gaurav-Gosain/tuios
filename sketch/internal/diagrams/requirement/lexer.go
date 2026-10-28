// Ported from pele src/diagrams/requirement/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
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

// lexer is the state of one run of tokenize: the tokens so far, the mode stack and the read position.
type lexer struct {
	src   string
	n     int
	out   Tokens
	stack []int
	state int
	p     int

	// Where each kind of direction statement starts and where its keyword ends, found in one pass.
	dirStarts [4][]int
	dirEnds   [4][]int
	anyDir    bool
	dirSeen   [4]int
	lineEnd   int
}

//go:noinline
func (l *lexer) emit(typ, s, e int) {
	l.out.types = append(l.out.types, typ)
	l.out.texts = append(l.out.texts, l.src[s:e])
	l.out.starts = append(l.out.starts, s)
	l.p = e
}

//go:noinline
func (l *lexer) push(s int) {
	l.stack = append(l.stack, l.state)
	l.state = s
}

//go:noinline
func (l *lexer) pop() {
	if len(l.stack) > 0 {
		l.state = l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
	}
}

func (l *lexer) lineBreak(q int) int {
	if e := strings.IndexByte(l.src[q:], '\n'); e >= 0 {
		return q + e
	}
	return l.n
}

// direction: `.*direction\s+XX[^\n]*`: the last such statement that starts on this line decides the token.
func (l *lexer) direction() bool {
	if l.lineEnd < l.p {
		l.lineEnd = l.n
		for q := l.p; q < l.n; q++ {
			c := l.src[q]
			if c == '\n' || c == '\r' {
				l.lineEnd = q
				break
			}
			if c == 0xe2 && q+2 < l.n && l.src[q+1] == 0x80 && (l.src[q+2] == 0xa8 || l.src[q+2] == 0xa9) {
				l.lineEnd = q
				break
			}
		}
		for k := 0; k < 4; k++ {
			list := l.dirStarts[k]
			j := l.dirSeen[k]
			for j < len(list) && list[j] <= l.lineEnd {
				j++
			}
			l.dirSeen[k] = j
		}
	}
	for k := 0; k < 4; k++ {
		j := l.dirSeen[k] - 1
		if j >= 0 && l.dirStarts[k][j] >= l.p {
			l.emit(T_direction_tb+k, l.p, l.lineBreak(l.dirEnds[k][j]))
			return true
		}
	}
	return false
}

func (l *lexer) isLineEndAt(q int) bool {
	c := l.src[q]
	if c < utf8.RuneSelf {
		return util.IsLineEnd(rune(c))
	}
	r, _ := utf8.DecodeRuneInString(l.src[q:])
	return util.IsLineEnd(r)
}

func (l *lexer) initial(c int) bool {
	lower := c | 32
	if lower == 116 && l.p+5 <= l.n && strings.ToLower(l.src[l.p+1:l.p+5]) == "itle" && isASCII(l.src[l.p+1:l.p+5]) {
		if s := spaceAt(l.src, l.p+5); s > 0 {
			from := l.p + 5 + s
			q := from
			for q < l.n {
				d := l.src[q]
				if d == 35 || d == 10 || d == 59 {
					break
				}
				q++
			}
			if q > from {
				l.emit(T_title, l.p, q)
				return true
			}
		}
	}
	if lower == 97 && byteAt(l.src, l.p+1)|32 == 99 && byteAt(l.src, l.p+2)|32 == 99 {
		if e := matchAcc(l.src, l.p, "acctitle", ':'); e >= 0 {
			l.push(S_acc_title)
			l.emit(T_acc_title, l.p, e)
			return true
		}
		if e := matchAcc(l.src, l.p, "accdescr", ':'); e >= 0 {
			l.push(S_acc_descr)
			l.emit(T_acc_descr, l.p, e)
			return true
		}
		if e := matchAcc(l.src, l.p, "accdescr", '{'); e >= 0 {
			l.push(S_acc_descr_multiline)
			l.p = e
			return true
		}
	}
	if l.anyDir && !l.isLineEndAt(l.p) && l.direction() {
		return true
	}

	if c == 10 || (c == 13 && byteAt(l.src, l.p+1) == 10) {
		q := l.p
		for q < l.n {
			d := l.src[q]
			if d == 10 {
				q++
			} else if d == 13 && byteAt(l.src, q+1) == 10 {
				q += 2
			} else {
				break
			}
		}
		l.emit(T_NEWLINE, l.p, q)
		return true
	}
	if s := spaceAt(l.src, l.p); s > 0 {
		l.p = spaces(l.src, l.p+s)
		return true
	}
	if c == 35 || c == 37 {
		l.p = l.lineBreak(l.p)
		return true
	}

	if c < utf8.RuneSelf && util.IsWord(rune(c)) {
		q := l.p + 1
		for isWordAt(l.src, q) {
			q++
		}
		if q-l.p <= LONGEST_KEYWORD {
			if kw, ok := keyword(strings.ToLower(l.src[l.p:q])); ok {
				if kw == T_STYLE || kw == T_CLASSDEF || kw == T_CLASS {
					l.push(S_style)
				}
				l.emit(kw, l.p, q)
				return true
			}
		}
		for q < l.n {
			d := l.src[q]
			if d < utf8.RuneSelf && isStop(d) {
				break
			}
			q++
		}
		l.out.types = append(l.out.types, T_unqString)
		l.out.texts = append(l.out.texts, js.Trim(l.src[l.p:q]))
		l.out.starts = append(l.out.starts, l.p)
		l.p = q
		return true
	}

	switch c {
	case 123:
		l.emit(T_STRUCT_START, l.p, l.p+1)
		return true
	case 125:
		l.emit(T_STRUCT_STOP, l.p, l.p+1)
		return true
	case 58:
		if byteAt(l.src, l.p+1) == 58 && byteAt(l.src, l.p+2) == 58 {
			l.emit(T_STYLE_SEPARATOR, l.p, l.p+3)
		} else {
			l.emit(T_COLONSEP, l.p, l.p+1)
		}
		return true
	case 60:
		if byteAt(l.src, l.p+1) != 45 {
			return false
		}
		l.emit(T_END_ARROW_L, l.p, l.p+2)
		return true
	case 45:
		if byteAt(l.src, l.p+1) == 62 {
			l.emit(T_END_ARROW_R, l.p, l.p+2)
		} else {
			l.emit(T_LINE, l.p, l.p+1)
		}
		return true
	case 34:
		l.push(S_string)
		l.p++
		return true
	case 44:
		l.emit(T_COMMA, l.p, l.p+1)
		return true
	}
	return false
}

func (l *lexer) style(c int) bool {
	if c < utf8.RuneSelf && util.IsWord(rune(c)) {
		q := l.p + 1
		for isWordAt(l.src, q) {
			q++
		}
		l.emit(T_ALPHA, l.p, q)
		return true
	}
	switch c {
	case 58:
		l.emit(T_COLON, l.p, l.p+1)
		return true
	case 59:
		l.emit(T_SEMICOLON, l.p, l.p+1)
		return true
	case 37:
		l.emit(T_PERCENT, l.p, l.p+1)
		return true
	case 45:
		l.emit(T_MINUS, l.p, l.p+1)
		return true
	case 35:
		l.emit(T_BRKT, l.p, l.p+1)
		return true
	case 32:
		l.p++
		return true
	case 34:
		l.push(S_string)
		l.p++
		return true
	case 10:
		l.pop()
		l.p++
		return true
	case 44:
		l.emit(T_COMMA, l.p, l.p+1)
		return true
	}
	return false
}

// tokenize produces the token stream of Mermaid's requirement lexer. Its rules are tried in order and
// the first match wins, so a line that holds a direction statement anywhere is one token.
func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src), state: S_INITIAL, lineEnd: -1}
	l.dirStarts, l.dirEnds, l.anyDir = directions(src)

	for l.p < l.n {
		c := int(l.src[l.p])
		ok := true
		switch l.state {
		case S_INITIAL:
			ok = l.initial(c)
		case S_style:
			ok = l.style(c)
		case S_string:
			if c == 34 {
				l.pop()
				l.p++
			} else if e := strings.IndexByte(l.src[l.p:], '"'); e >= 0 {
				l.emit(T_qString, l.p, l.p+e)
			} else {
				l.emit(T_qString, l.p, l.n)
			}
		case S_acc_title:
			l.pop()
			l.emit(T_acc_title_value, l.p, l.lineBreak(l.p))
		case S_acc_descr:
			l.pop()
			l.emit(T_acc_descr_value, l.p, l.lineBreak(l.p))
		case S_acc_descr_multiline:
			if c == 125 {
				l.pop()
				l.p++
			} else if e := strings.IndexByte(l.src[l.p:], '}'); e >= 0 {
				l.emit(T_acc_descr_multiline_value, l.p, l.p+e)
			} else {
				l.emit(T_acc_descr_multiline_value, l.p, l.n)
			}
		}
		if !ok {
			l.out.types = append(l.out.types, T_ERROR)
			l.out.texts = append(l.out.texts, "")
			l.out.starts = append(l.out.starts, l.p)
			return l.out
		}
	}

	// At the end of input Jison still tries the rules that can match nothing, once.
	last := -1
	switch l.state {
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
		l.emit(last, l.n, l.n)
	}
	l.emit(T_END, l.n, l.n)
	return l.out
}
