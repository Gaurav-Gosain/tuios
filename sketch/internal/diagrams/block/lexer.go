// Ported from pele src/diagrams/block/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The port reads UTF-8 bytes. Every character the lexer matches is ASCII, except
// whitespace, which is read as a whole character. A byte offset stands for pele's unit
// offset.

package block

import (
	"strings"
	"unicode/utf8"

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
	S_md_string
	S_NODE
	S_BLOCK_ARROW
	S_ARROW_DIR
	S_LLABEL
	S_CLASSDEF
	S_CLASSDEFID
	S_CLASS
	S_CLASS_STYLE
	S_STYLE_STMNT
	S_STYLE_DEFINITION
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
)

var DIRECTIONS = [...]string{"right", "left", "x", "y", "up", "down"}

// isIDStop: characters that end a node id: ( ) [ { } < > - : = and whitespace.
func isIDStop(c byte) bool {
	switch c {
	case 9, 10, 11, 12, 13, 32, 40, 41, 45, 58, 60, 61, 62, 91, 123, 125:
		return true
	}
	return false
}

// byteAt is the byte at q, or -1 past the end, where pele's charCodeAt gives NaN.
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
	c := src[q]
	if c < utf8.RuneSelf {
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
}

//go:noinline
func (l *lexer) put(typ int, text string, end int) bool {
	l.out.types = append(l.out.types, typ)
	l.out.texts = append(l.out.texts, text)
	l.out.starts = append(l.out.starts, l.p)
	l.p = end
	return true
}

//go:noinline
func (l *lexer) emit(typ int, end int) bool { return l.put(typ, l.src[l.p:end], end) }

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

func (l *lexer) ws(q int) int {
	for q < l.n {
		s := spaceAt(l.src, q)
		if s == 0 {
			break
		}
		q += s
	}
	return q
}

//go:noinline
func (l *lexer) digits(q int) int {
	for q < l.n {
		c := l.src[q]
		if c < 48 || c > 57 {
			break
		}
		q++
	}
	return q
}

//go:noinline
func (l *lexer) word(q int) int {
	for isWordAt(l.src, q) {
		q++
	}
	return q
}

//go:noinline
func (l *lexer) startsWith(text string, q int) bool {
	return q <= l.n && strings.HasPrefix(l.src[q:], text)
}

func (l *lexer) keyword(text string) bool {
	return l.startsWith(text, l.p) && !isWordAt(l.src, l.p+len(text))
}

//go:noinline
func (l *lexer) quote() bool {
	if byteAt(l.src, l.p+1) == 96 {
		l.push(S_md_string)
		l.p += 2
	} else {
		l.push(S_string)
		l.p++
	}
	return true
}

// dotted: end of `.+-[xo>]?` at q, or -1.
func (l *lexer) dotted(q int) int {
	if byteAt(l.src, q) != 46 {
		return -1
	}
	q++
	for byteAt(l.src, q) == 46 {
		q++
	}
	if byteAt(l.src, q) != 45 {
		return -1
	}
	c := byteAt(l.src, q+1)
	if c == 120 || c == 111 || c == 62 {
		return q + 2
	}
	return q + 1
}

// linkEnd: end of a complete link at q, before its trailing whitespace, or -1.
func (l *lexer) linkEnd(q int) int {
	c := byteAt(l.src, q)
	if c == 120 || c == 111 || c == 60 {
		q++
	}
	line := byteAt(l.src, q)
	if line == 46 {
		return l.dotted(q)
	}
	if line != 45 && line != 61 {
		return -1
	}
	k := q + 1
	for byteAt(l.src, k) == line {
		k++
	}
	if k-q == 1 {
		if line == 45 {
			return l.dotted(k)
		}
		return -1
	}
	head := byteAt(l.src, k)
	if head == 120 || head == 111 || head == 62 {
		return k + 1
	}
	if k-q >= 3 {
		return k
	}
	return -1
}

func (l *lexer) link() bool {
	if e := l.linkEnd(l.p); e >= 0 {
		return l.emit(T_LINK, l.ws(e))
	}
	q := l.p
	if byteAt(l.src, l.p) == 60 {
		q = l.p + 1
	}
	a := byteAt(l.src, q)
	b := byteAt(l.src, q+1)
	if (a == 45 && (b == 45 || b == 46)) || (a == 61 && b == 61) {
		l.push(S_LLABEL)
		return l.emit(T_START_LINK, l.ws(q+2))
	}
	return false
}

func (l *lexer) lineState(typ int) bool {
	e := strings.IndexByte(l.src[l.p:], '\n')
	if e == -1 {
		e = l.n
	} else {
		e += l.p
	}
	l.pop()
	return l.emit(typ, e)
}

// idList: `\w+(,\s*\w+)*`
func (l *lexer) idList(typ, next int) bool {
	k := l.word(l.p)
	if k == l.p {
		return false
	}
	for byteAt(l.src, k) == 44 {
		from := l.ws(k + 1)
		to := l.word(from)
		if to == from {
			break
		}
		k = to
	}
	l.state = next
	return l.emit(typ, k)
}

func (l *lexer) initial(c int) bool {
	if s := spaceAt(l.src, l.p); s > 0 {
		l.p = l.ws(l.p + s)
		return true
	}
	var q int
	switch c {
	case 98:
		if l.startsWith("block", l.p) {
			if l.keyword("block-beta") {
				return l.emit(T_KEY, l.p+10)
			}
			if byteAt(l.src, l.p+5) == 58 {
				return l.emit(T_ID_BLOCK, l.p+6)
			}
			if !isWordAt(l.src, l.p+5) {
				return l.emit(T_KEY, l.p+5)
			}
		}
	case 99:
		if l.startsWith("columns", l.p) {
			q = l.ws(l.p + 7)
			if q > l.p+7 {
				if l.startsWith("auto", q) && !isWordAt(l.src, q+4) {
					return l.put(T_COLUMNS, "-1", q+4)
				}
				if e := l.digits(q); e > q {
					return l.put(T_COLUMNS, l.src[q:e], e)
				}
			}
		} else if l.startsWith("class", l.p) {
			def := l.startsWith("Def", l.p+5)
			from := l.p + 5
			if def {
				from = l.p + 8
			}
			q = l.ws(from)
			if q > from {
				if def {
					l.push(S_CLASSDEF)
					return l.emit(T_CLASSDEF, q)
				}
				l.push(S_CLASS)
				return l.emit(T_CLASS, q)
			}
		}
	case 34:
		return l.quote()
	case 115:
		if l.startsWith("space", l.p) {
			if byteAt(l.src, l.p+5) == 58 {
				if e := l.digits(l.p + 6); e > l.p+6 {
					return l.put(T_SPACE_BLOCK, l.src[l.p+6:e], e)
				}
			}
			if !isWordAt(l.src, l.p+5) {
				return l.put(T_SPACE_BLOCK, "1", l.p+5)
			}
		} else if l.startsWith("style", l.p) {
			q = l.ws(l.p + 5)
			if q > l.p+5 {
				l.push(S_STYLE_STMNT)
				return l.emit(T_STYLE, q)
			}
		}
	case 100:
		if l.keyword("default") {
			return l.emit(T_DEFAULT, l.p+7)
		}
	case 108:
		if l.keyword("linkStyle") {
			return l.emit(T_LINKSTYLE, l.p+9)
		}
	case 105:
		if l.keyword("interpolate") {
			return l.emit(T_INTERPOLATE, l.p+11)
		}
	case 97:
		if l.startsWith("accTitle", l.p) {
			q = l.ws(l.p + 8)
			if byteAt(l.src, q) == 58 {
				l.push(S_acc_title)
				return l.emit(T_acc_title, l.ws(q+1))
			}
		} else if l.startsWith("accDescr", l.p) {
			q = l.ws(l.p + 8)
			d := byteAt(l.src, q)
			if d == 58 {
				l.push(S_acc_descr)
				return l.emit(T_acc_descr, l.ws(q+1))
			}
			if d == 123 {
				l.push(S_acc_descr_multiline)
				l.p = l.ws(q + 1)
				return true
			}
		}
	case 101:
		if l.keyword("end") {
			return l.emit(T_end, l.ws(l.p+3))
		}
	case 45:
		if byteAt(l.src, l.p+1) == 41 {
			l.push(S_NODE)
			return l.emit(T_NODE_DSTART, l.p+2)
		}
		return l.link()
	case 61:
		return l.link()
	case 60:
		if byteAt(l.src, l.p+1) == 91 {
			l.push(S_BLOCK_ARROW)
			return l.emit(T_BLOCK_ARROW_START, l.p+2)
		}
		return l.link()
	case 40:
		d := byteAt(l.src, l.p+1)
		w := 1
		if d == 45 || d == 91 {
			w = 2
		} else if d == 40 {
			w = 2
			if byteAt(l.src, l.p+2) == 40 {
				w = 3
			}
		}
		l.push(S_NODE)
		return l.emit(T_NODE_DSTART, l.p+w)
	case 41, 123:
		l.push(S_NODE)
		if byteAt(l.src, l.p+1) == c {
			return l.emit(T_NODE_DSTART, l.p+2)
		}
		return l.emit(T_NODE_DSTART, l.p+1)
	case 62:
		l.push(S_NODE)
		return l.emit(T_NODE_DSTART, l.p+1)
	case 91:
		d := byteAt(l.src, l.p+1)
		l.push(S_NODE)
		if d == 91 || d == 124 || d == 40 || d == 92 || d == 47 {
			return l.emit(T_NODE_DSTART, l.p+2)
		}
		return l.emit(T_NODE_DSTART, l.p+1)
	case 58:
		if e := l.digits(l.p + 1); e > l.p+1 {
			return l.put(T_SIZE, l.src[l.p+1:e], e)
		}
		return false
	}

	q = l.p
	for q < l.n {
		ch := l.src[q]
		if ch < utf8.RuneSelf {
			if isIDStop(ch) {
				break
			}
			q++
			continue
		}
		if spaceAt(l.src, q) > 0 {
			break
		}
		_, size := utf8.DecodeRuneInString(l.src[q:])
		q += size
	}
	return q > l.p && l.emit(T_NODE_ID, q)
}

func (l *lexer) nodeEnd(c int) bool {
	d := byteAt(l.src, l.p+1)
	w := 0
	switch c {
	case 34:
		return l.quote()
	case 40:
		w = 1
		if d == 45 {
			w = 2
		} else if d == 40 {
			w = 2
			if byteAt(l.src, l.p+2) == 40 {
				w = 3
			}
		}
	case 41:
		w = 1
		if d == 41 {
			w = 2
			if byteAt(l.src, l.p+2) == 41 {
				w = 3
			}
		} else if d == 93 {
			w = 2
		}
	case 125:
		w = 1
		if d == 125 {
			w = 2
		}
	case 45:
		if d == 41 {
			w = 2
		}
	case 93:
		w = 1
		if d == 93 || d == 41 || d == 62 {
			w = 2
		}
	case 92, 47:
		if d == 93 {
			w = 2
		}
	}
	if w == 0 {
		return false
	}
	l.pop()
	return l.emit(T_NODE_DEND, l.p+w)
}

func (l *lexer) arrowDir(c int) bool {
	if c == 41 {
		l.pop()
		l.pop()
		return l.put(T_BLOCK_ARROW_END, "]>", l.ws(l.p+1))
	}
	from := l.ws(l.p)
	if c == 44 {
		from = l.ws(l.p + 1)
	}
	for _, dir := range DIRECTIONS {
		if l.startsWith(dir, from) {
			e := l.ws(from + len(dir))
			if c == 44 {
				return l.put(T_DIR, l.src[from:e], e)
			}
			return l.put(T_DIR, l.src[l.p:e], e)
		}
	}
	return false
}

func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src), state: S_INITIAL}
	for {
		if l.p >= l.n {
			typ := -1
			switch l.state {
			case S_INITIAL:
				typ = T_EOF
			case S_string:
				typ = T_STR
			case S_CLASSDEFID:
				typ = T_CLASSDEF_STYLEOPTS
			case S_CLASS_STYLE:
				typ = T_STYLECLASS
			case S_STYLE_DEFINITION:
				typ = T_STYLE_DEFINITION_DATA
			case S_acc_title:
				typ = T_acc_title_value
			case S_acc_descr:
				typ = T_acc_descr_value
			case S_acc_descr_multiline:
				typ = T_acc_descr_multiline_value
			}
			l.p = l.n
			if typ != -1 {
				l.put(typ, "", l.n)
			}
			break
		}

		c := int(l.src[l.p])
		ok := true

		switch l.state {
		case S_INITIAL:
			ok = l.initial(c)

		case S_string:
			if c == 34 {
				l.pop()
				l.p++
			} else {
				e := strings.IndexByte(l.src[l.p:], '"')
				if e == -1 {
					l.emit(T_STR, l.n)
				} else {
					l.emit(T_STR, l.p+e)
				}
			}

		case S_md_string:
			if c == 96 {
				ok = byteAt(l.src, l.p+1) == 34
				if ok {
					l.pop()
					l.p += 2
				}
			} else if c == 34 {
				ok = false
			} else {
				q := l.p + 1
				for q < l.n {
					ch := l.src[q]
					if ch == 96 || ch == 34 {
						break
					}
					q++
				}
				l.emit(T_MD_STR, q)
			}

		case S_NODE:
			ok = l.nodeEnd(c)

		case S_BLOCK_ARROW:
			if c == 34 {
				l.quote()
			} else if c == 93 && byteAt(l.src, l.p+1) == 62 {
				q := l.ws(l.p + 2)
				ok = byteAt(l.src, q) == 40
				if ok {
					l.push(S_ARROW_DIR)
					l.p = q + 1
				}
			} else {
				ok = false
			}

		case S_ARROW_DIR:
			ok = l.arrowDir(c)

		case S_LLABEL:
			if c == 34 {
				if byteAt(l.src, l.p+1) == 96 {
					l.quote()
				} else {
					l.emit(T_LINK_LABEL, l.p+1)
					l.push(S_string)
				}
			} else {
				e := l.linkEnd(l.ws(l.p))
				ok = e >= 0
				if ok {
					l.pop()
					l.emit(T_LINK, l.ws(e))
				}
			}

		case S_CLASSDEF:
			typ := T_DEFAULT_CLASSDEF_ID
			k := l.p
			if l.startsWith("DEFAULT", l.p) {
				k = l.p + 7
			}
			q := l.ws(k)
			if k == l.p || q == k {
				typ = T_CLASSDEF_ID
				k = l.word(l.p)
				q = l.ws(k)
			}
			ok = k > l.p && q > k
			if ok {
				l.state = S_CLASSDEFID
				l.emit(typ, q)
			}

		case S_CLASSDEFID:
			l.lineState(T_CLASSDEF_STYLEOPTS)

		case S_CLASS:
			ok = l.idList(T_CLASSENTITY_IDS, S_CLASS_STYLE)

		case S_CLASS_STYLE:
			l.lineState(T_STYLECLASS)

		case S_STYLE_STMNT:
			ok = l.idList(T_STYLE_ENTITY_IDS, S_STYLE_DEFINITION)

		case S_STYLE_DEFINITION:
			l.lineState(T_STYLE_DEFINITION_DATA)

		case S_acc_title:
			l.lineState(T_acc_title_value)

		case S_acc_descr:
			l.lineState(T_acc_descr_value)

		case S_acc_descr_multiline:
			if c == 125 {
				l.pop()
				l.p++
			} else {
				e := strings.IndexByte(l.src[l.p:], '}')
				if e == -1 {
					l.emit(T_acc_descr_multiline_value, l.n)
				} else {
					l.emit(T_acc_descr_multiline_value, l.p+e)
				}
			}
		}

		if !ok {
			l.out.types = append(l.out.types, T_ERROR)
			l.out.texts = append(l.out.texts, "")
			l.out.starts = append(l.out.starts, l.p)
			return l.out
		}
	}

	l.out.types = append(l.out.types, T_END)
	l.out.texts = append(l.out.texts, "")
	l.out.starts = append(l.out.starts, l.n)
	return l.out
}
