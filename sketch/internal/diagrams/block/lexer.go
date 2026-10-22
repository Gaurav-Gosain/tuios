// Ported from pele src/diagrams/block/lexer.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
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

func tokenize(src string) Tokens {
	n := len(src)
	var out Tokens
	var stack []int
	state := S_INITIAL
	p := 0

	put := func(typ int, text string, end int) bool {
		out.types = append(out.types, typ)
		out.texts = append(out.texts, text)
		out.starts = append(out.starts, p)
		p = end
		return true
	}
	emit := func(typ int, end int) bool { return put(typ, src[p:end], end) }
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
	ws := func(q int) int {
		for q < n {
			s := spaceAt(src, q)
			if s == 0 {
				break
			}
			q += s
		}
		return q
	}
	digits := func(q int) int {
		for q < n {
			c := src[q]
			if c < 48 || c > 57 {
				break
			}
			q++
		}
		return q
	}
	word := func(q int) int {
		for isWordAt(src, q) {
			q++
		}
		return q
	}
	startsWith := func(text string, q int) bool { return q <= n && strings.HasPrefix(src[q:], text) }
	keyword := func(text string) bool { return startsWith(text, p) && !isWordAt(src, p+len(text)) }

	quote := func() bool {
		if byteAt(src, p+1) == 96 {
			push(S_md_string)
			p += 2
		} else {
			push(S_string)
			p++
		}
		return true
	}

	// dotted: end of `.+-[xo>]?` at q, or -1.
	dotted := func(q int) int {
		if byteAt(src, q) != 46 {
			return -1
		}
		q++
		for byteAt(src, q) == 46 {
			q++
		}
		if byteAt(src, q) != 45 {
			return -1
		}
		c := byteAt(src, q+1)
		if c == 120 || c == 111 || c == 62 {
			return q + 2
		}
		return q + 1
	}

	// linkEnd: end of a complete link at q, before its trailing whitespace, or -1.
	linkEnd := func(q int) int {
		c := byteAt(src, q)
		if c == 120 || c == 111 || c == 60 {
			q++
		}
		line := byteAt(src, q)
		if line == 46 {
			return dotted(q)
		}
		if line != 45 && line != 61 {
			return -1
		}
		k := q + 1
		for byteAt(src, k) == line {
			k++
		}
		if k-q == 1 {
			if line == 45 {
				return dotted(k)
			}
			return -1
		}
		head := byteAt(src, k)
		if head == 120 || head == 111 || head == 62 {
			return k + 1
		}
		if k-q >= 3 {
			return k
		}
		return -1
	}

	link := func() bool {
		if e := linkEnd(p); e >= 0 {
			return emit(T_LINK, ws(e))
		}
		q := p
		if byteAt(src, p) == 60 {
			q = p + 1
		}
		a := byteAt(src, q)
		b := byteAt(src, q+1)
		if (a == 45 && (b == 45 || b == 46)) || (a == 61 && b == 61) {
			push(S_LLABEL)
			return emit(T_START_LINK, ws(q+2))
		}
		return false
	}

	lineState := func(typ int) bool {
		e := strings.IndexByte(src[p:], '\n')
		if e == -1 {
			e = n
		} else {
			e += p
		}
		pop()
		return emit(typ, e)
	}

	// idList: `\w+(,\s*\w+)*`
	idList := func(typ, next int) bool {
		k := word(p)
		if k == p {
			return false
		}
		for byteAt(src, k) == 44 {
			from := ws(k + 1)
			to := word(from)
			if to == from {
				break
			}
			k = to
		}
		state = next
		return emit(typ, k)
	}

	initial := func(c int) bool {
		if s := spaceAt(src, p); s > 0 {
			p = ws(p + s)
			return true
		}
		var q int
		switch c {
		case 98:
			if startsWith("block", p) {
				if keyword("block-beta") {
					return emit(T_KEY, p+10)
				}
				if byteAt(src, p+5) == 58 {
					return emit(T_ID_BLOCK, p+6)
				}
				if !isWordAt(src, p+5) {
					return emit(T_KEY, p+5)
				}
			}
		case 99:
			if startsWith("columns", p) {
				q = ws(p + 7)
				if q > p+7 {
					if startsWith("auto", q) && !isWordAt(src, q+4) {
						return put(T_COLUMNS, "-1", q+4)
					}
					if e := digits(q); e > q {
						return put(T_COLUMNS, src[q:e], e)
					}
				}
			} else if startsWith("class", p) {
				def := startsWith("Def", p+5)
				from := p + 5
				if def {
					from = p + 8
				}
				q = ws(from)
				if q > from {
					if def {
						push(S_CLASSDEF)
						return emit(T_CLASSDEF, q)
					}
					push(S_CLASS)
					return emit(T_CLASS, q)
				}
			}
		case 34:
			return quote()
		case 115:
			if startsWith("space", p) {
				if byteAt(src, p+5) == 58 {
					if e := digits(p + 6); e > p+6 {
						return put(T_SPACE_BLOCK, src[p+6:e], e)
					}
				}
				if !isWordAt(src, p+5) {
					return put(T_SPACE_BLOCK, "1", p+5)
				}
			} else if startsWith("style", p) {
				q = ws(p + 5)
				if q > p+5 {
					push(S_STYLE_STMNT)
					return emit(T_STYLE, q)
				}
			}
		case 100:
			if keyword("default") {
				return emit(T_DEFAULT, p+7)
			}
		case 108:
			if keyword("linkStyle") {
				return emit(T_LINKSTYLE, p+9)
			}
		case 105:
			if keyword("interpolate") {
				return emit(T_INTERPOLATE, p+11)
			}
		case 97:
			if startsWith("accTitle", p) {
				q = ws(p + 8)
				if byteAt(src, q) == 58 {
					push(S_acc_title)
					return emit(T_acc_title, ws(q+1))
				}
			} else if startsWith("accDescr", p) {
				q = ws(p + 8)
				d := byteAt(src, q)
				if d == 58 {
					push(S_acc_descr)
					return emit(T_acc_descr, ws(q+1))
				}
				if d == 123 {
					push(S_acc_descr_multiline)
					p = ws(q + 1)
					return true
				}
			}
		case 101:
			if keyword("end") {
				return emit(T_end, ws(p+3))
			}
		case 45:
			if byteAt(src, p+1) == 41 {
				push(S_NODE)
				return emit(T_NODE_DSTART, p+2)
			}
			return link()
		case 61:
			return link()
		case 60:
			if byteAt(src, p+1) == 91 {
				push(S_BLOCK_ARROW)
				return emit(T_BLOCK_ARROW_START, p+2)
			}
			return link()
		case 40:
			d := byteAt(src, p+1)
			l := 1
			if d == 45 || d == 91 {
				l = 2
			} else if d == 40 {
				l = 2
				if byteAt(src, p+2) == 40 {
					l = 3
				}
			}
			push(S_NODE)
			return emit(T_NODE_DSTART, p+l)
		case 41, 123:
			push(S_NODE)
			if byteAt(src, p+1) == c {
				return emit(T_NODE_DSTART, p+2)
			}
			return emit(T_NODE_DSTART, p+1)
		case 62:
			push(S_NODE)
			return emit(T_NODE_DSTART, p+1)
		case 91:
			d := byteAt(src, p+1)
			push(S_NODE)
			if d == 91 || d == 124 || d == 40 || d == 92 || d == 47 {
				return emit(T_NODE_DSTART, p+2)
			}
			return emit(T_NODE_DSTART, p+1)
		case 58:
			if e := digits(p + 1); e > p+1 {
				return put(T_SIZE, src[p+1:e], e)
			}
			return false
		}

		q = p
		for q < n {
			ch := src[q]
			if ch < utf8.RuneSelf {
				if isIDStop(ch) {
					break
				}
				q++
				continue
			}
			if spaceAt(src, q) > 0 {
				break
			}
			_, size := utf8.DecodeRuneInString(src[q:])
			q += size
		}
		return q > p && emit(T_NODE_ID, q)
	}

	nodeEnd := func(c int) bool {
		d := byteAt(src, p+1)
		l := 0
		switch c {
		case 34:
			return quote()
		case 40:
			l = 1
			if d == 45 {
				l = 2
			} else if d == 40 {
				l = 2
				if byteAt(src, p+2) == 40 {
					l = 3
				}
			}
		case 41:
			l = 1
			if d == 41 {
				l = 2
				if byteAt(src, p+2) == 41 {
					l = 3
				}
			} else if d == 93 {
				l = 2
			}
		case 125:
			l = 1
			if d == 125 {
				l = 2
			}
		case 45:
			if d == 41 {
				l = 2
			}
		case 93:
			l = 1
			if d == 93 || d == 41 || d == 62 {
				l = 2
			}
		case 92, 47:
			if d == 93 {
				l = 2
			}
		}
		if l == 0 {
			return false
		}
		pop()
		return emit(T_NODE_DEND, p+l)
	}

	arrowDir := func(c int) bool {
		if c == 41 {
			pop()
			pop()
			return put(T_BLOCK_ARROW_END, "]>", ws(p+1))
		}
		from := ws(p)
		if c == 44 {
			from = ws(p + 1)
		}
		for _, dir := range DIRECTIONS {
			if startsWith(dir, from) {
				e := ws(from + len(dir))
				if c == 44 {
					return put(T_DIR, src[from:e], e)
				}
				return put(T_DIR, src[p:e], e)
			}
		}
		return false
	}

	for {
		if p >= n {
			typ := -1
			switch state {
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
			p = n
			if typ != -1 {
				put(typ, "", n)
			}
			break
		}

		c := int(src[p])
		ok := true

		switch state {
		case S_INITIAL:
			ok = initial(c)

		case S_string:
			if c == 34 {
				pop()
				p++
			} else {
				e := strings.IndexByte(src[p:], '"')
				if e == -1 {
					emit(T_STR, n)
				} else {
					emit(T_STR, p+e)
				}
			}

		case S_md_string:
			if c == 96 {
				ok = byteAt(src, p+1) == 34
				if ok {
					pop()
					p += 2
				}
			} else if c == 34 {
				ok = false
			} else {
				q := p + 1
				for q < n {
					ch := src[q]
					if ch == 96 || ch == 34 {
						break
					}
					q++
				}
				emit(T_MD_STR, q)
			}

		case S_NODE:
			ok = nodeEnd(c)

		case S_BLOCK_ARROW:
			if c == 34 {
				quote()
			} else if c == 93 && byteAt(src, p+1) == 62 {
				q := ws(p + 2)
				ok = byteAt(src, q) == 40
				if ok {
					push(S_ARROW_DIR)
					p = q + 1
				}
			} else {
				ok = false
			}

		case S_ARROW_DIR:
			ok = arrowDir(c)

		case S_LLABEL:
			if c == 34 {
				if byteAt(src, p+1) == 96 {
					quote()
				} else {
					emit(T_LINK_LABEL, p+1)
					push(S_string)
				}
			} else {
				e := linkEnd(ws(p))
				ok = e >= 0
				if ok {
					pop()
					emit(T_LINK, ws(e))
				}
			}

		case S_CLASSDEF:
			typ := T_DEFAULT_CLASSDEF_ID
			k := p
			if startsWith("DEFAULT", p) {
				k = p + 7
			}
			q := ws(k)
			if k == p || q == k {
				typ = T_CLASSDEF_ID
				k = word(p)
				q = ws(k)
			}
			ok = k > p && q > k
			if ok {
				state = S_CLASSDEFID
				emit(typ, q)
			}

		case S_CLASSDEFID:
			lineState(T_CLASSDEF_STYLEOPTS)

		case S_CLASS:
			ok = idList(T_CLASSENTITY_IDS, S_CLASS_STYLE)

		case S_CLASS_STYLE:
			lineState(T_STYLECLASS)

		case S_STYLE_STMNT:
			ok = idList(T_STYLE_ENTITY_IDS, S_STYLE_DEFINITION)

		case S_STYLE_DEFINITION:
			lineState(T_STYLE_DEFINITION_DATA)

		case S_acc_title:
			lineState(T_acc_title_value)

		case S_acc_descr:
			lineState(T_acc_descr_value)

		case S_acc_descr_multiline:
			if c == 125 {
				pop()
				p++
			} else {
				e := strings.IndexByte(src[p:], '}')
				if e == -1 {
					emit(T_acc_descr_multiline_value, n)
				} else {
					emit(T_acc_descr_multiline_value, p+e)
				}
			}
		}

		if !ok {
			out.types = append(out.types, T_ERROR)
			out.texts = append(out.texts, "")
			out.starts = append(out.starts, p)
			return out
		}
	}

	out.types = append(out.types, T_END)
	out.texts = append(out.texts, "")
	out.starts = append(out.starts, n)
	return out
}
