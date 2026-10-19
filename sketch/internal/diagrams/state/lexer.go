// Ported from pele src/diagrams/state/lexer.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// As in the flowchart lexer, the port reads UTF-8 bytes: code() decodes the
// character at a byte offset, and a step past a character that may not be
// ASCII steps the whole character. pele's regular expressions are written out
// as hand matchers, since Go's case folding (?i) also folds the Kelvin sign
// into k.

package state

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
	// Set when lexing stopped on the grammar's own error rather than on unrecognized text. "" for none.
	error string
}

// add appends a token. It is kept out of line, as the lexer emits from many places.
//
//go:noinline
func (t *Tokens) add(typ int, text string, start int) {
	t.types = append(t.types, typ)
	t.texts = append(t.texts, text)
	t.starts = append(t.starts, start)
}

// Lexer states.
const (
	S_INITIAL = iota
	S_struct
	S_STATE
	S_STATE_STRING
	S_STATE_ID
	S_SCALE
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
	S_CLASSDEF
	S_CLASSDEFID
	S_CLASS
	S_CLASS_STYLE
	S_STYLE
	S_STYLEDEF_STYLES
	S_NOTE
	S_NOTE_ID
	S_NOTE_TEXT
	S_FLOATING_NOTE
	S_FLOATING_NOTE_ID
)

func indexFrom(s, sub string, from int) int {
	if from > len(s) {
		return -1
	}
	if from < 0 {
		from = 0
	}
	i := strings.Index(s[from:], sub)
	if i < 0 {
		return -1
	}
	return from + i
}

// The helpers below are kept out of line: the lexer calls them from many places, and
// inlining each copy doubles its size in the binary.

// codeAt decodes the character at a byte offset: -1 past the end, 0xfffe for a broken
// sequence, and 0xd800 for a character outside the BMP, which pele reads as a surrogate.
//
//go:noinline
func codeAt(src string, q int) rune {
	if q < 0 || q >= len(src) {
		return -1
	}
	b := src[q]
	if b < utf8.RuneSelf {
		return rune(b)
	}
	r, _ := utf8.DecodeRuneInString(src[q:])
	if r == utf8.RuneError {
		return 0xfffe
	}
	if r > 0xffff {
		return 0xd800
	}
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

// wsEndAt is the end of the whitespace at q.
//
//go:noinline
func wsEndAt(src string, q int) int {
	j := q
	for j < len(src) && util.IsSpace(codeAt(src, j)) {
		j += sizeAt(src, j)
	}
	return j
}

// ciAt matches a lowercase ASCII literal at q without regard to case, as the /i flag does for ASCII.
//
//go:noinline
func ciAt(src string, q int, w string) bool {
	if q < 0 || q+len(w) > len(src) {
		return false
	}
	for i := 0; i < len(w); i++ {
		a := w[i]
		c := src[q+i]
		if c != a && !(a >= 97 && a <= 122 && c == a-32) {
			return false
		}
	}
	return true
}

// RE_DIRECTION: /direction/gi.
func reDirection(src string, from int) int {
	for i := max(from, 0); i+9 <= len(src); i++ {
		if src[i]|32 == 'd' && ciAt(src, i, "direction") {
			return i
		}
	}
	return -1
}

// RE_STEREOTYPE: /<<(?:fork|join|choice)>>|\[\[(?:fork|join|choice)\]\]/gi.
func reStereotype(src string, from int) int {
	for i := max(from, 0); i+8 <= len(src); i++ {
		var close string
		switch {
		case src[i] == '<' && src[i+1] == '<':
			close = ">>"
		case src[i] == '[' && src[i+1] == '[':
			close = "]]"
		default:
			continue
		}
		for _, w := range [...]string{"fork", "join", "choice"} {
			if ciAt(src, i+2, w) && strings.HasPrefix(src[i+2+len(w):], close) {
				return i
			}
		}
	}
	return -1
}

// RE_LINE_END: /[\n\r  ]/g.
func reLineEnd(src string, from int) int {
	for i := max(from, 0); i < len(src); i++ {
		c := src[i]
		if c == '\n' || c == '\r' {
			return i
		}
		if c == 0xe2 && i+2 < len(src) && src[i+1] == 0x80 && (src[i+2] == 0xa8 || src[i+2] == 0xa9) {
			return i
		}
	}
	return -1
}

// Finds the next match of a pattern at or after a position, remembering the answer so that
// asking at each token costs one scan of the text in total.
func finder(match func(src string, from int) int, src string) func(q int) int {
	from := 0
	next := -2
	return func(q int) int {
		if next == -2 || q < from || (next != -1 && q > next) {
			if q > len(src) {
				next = -1
			} else {
				next = match(src, q)
			}
			from = q
		}
		return next
	}
}

func tokenize(src string) Tokens {
	n := len(src)
	toks := &Tokens{}
	var stack []int
	state := S_INITIAL
	p := 0
	errorText := ""

	nextDirection := finder(reDirection, src)
	nextStereotype := finder(reStereotype, src)
	nextLineEnd := finder(reLineEnd, src)
	nextPercent := finder(func(s string, from int) int { return indexFrom(s, "%%", from) }, src)
	nextBrace := finder(func(s string, from int) int { return indexFrom(s, "{", from) }, src)

	// The last `direction <dir>` of each kind on the line being lexed, and where its keyword ends.
	dirFrom := -1
	dirTo := -1
	dirAt := [4]int{-1, -1, -1, -1}
	dirEnd := [4]int{}
	// The same for <<fork>>, <<join>>, <<choice>> and their [[ ]] spellings.
	stereoFrom := -1
	stereoTo := -1
	stereoAt := [6]int{-1, -1, -1, -1, -1, -1}

	code := func(q int) rune { return codeAt(src, q) }
	size := func(q int) int { return sizeAt(src, q) }
	isSpace := util.IsSpace
	isWord := util.IsWord

	emit := func(typ int, s, e int) {
		toks.add(typ, src[s:e], s)
		p = e
	}
	emitText := func(typ int, text string, s, e int) {
		toks.add(typ, text, s)
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
	// Case-insensitive match of a lowercase ASCII word, as the grammar's /i flag gives.
	word := func(w string, q int) bool { return ciAt(src, q, w) }
	wsEnd := func(q int) int { return wsEndAt(src, q) }
	wordEnd := func(q int) int {
		j := q
		for j < n && isWord(code(j)) {
			j++
		}
		return j
	}
	eol := func(q int) int {
		k := indexFrom(src, "\n", q)
		if k == -1 {
			return n
		}
		return k
	}
	lineEnd := func(q int) int {
		k := nextLineEnd(q)
		if k == -1 {
			return n
		}
		return k
	}

	// Rules `.*direction\s+TB[^\n]*` and its three siblings: the first kind, in grammar order,
	// found between q and the end of its line.
	direction := func(q int) int {
		if q < dirFrom || q > dirTo {
			first := nextDirection(q)
			end := n
			if first != -1 {
				end = lineEnd(q)
			}
			dirFrom = q
			dirTo = end
			dirAt = [4]int{-1, -1, -1, -1}
			for d := first; d != -1 && d < end; d = nextDirection(d + 1) {
				w := wsEnd(d + 9)
				if w == d+9 {
					continue
				}
				kind := -1
				switch {
				case word("tb", w):
					kind = 0
				case word("bt", w):
					kind = 1
				case word("rl", w):
					kind = 2
				case word("lr", w):
					kind = 3
				}
				if kind == -1 {
					continue
				}
				dirAt[kind] = d
				dirEnd[kind] = w + 2
			}
		}
		for kind := 0; kind < 4; kind++ {
			if dirAt[kind] >= q {
				return kind
			}
		}
		return -1
	}

	stereotype := func(q int) int {
		if q < stereoFrom || q > stereoTo {
			first := nextStereotype(q)
			end := n
			if first != -1 {
				end = lineEnd(q)
			}
			stereoFrom = q
			stereoTo = end
			stereoAt = [6]int{-1, -1, -1, -1, -1, -1}
			for d := first; d != -1 && d < end; d = nextStereotype(d + 1) {
				letter := src[d+2] | 32
				k := 3
				if src[d] == 60 {
					k = 0
				}
				switch letter {
				case 102:
				case 106:
					k++
				default:
					k += 2
				}
				stereoAt[k] = d
			}
		}
		for kind := 0; kind < 6; kind++ {
			if stereoAt[kind] >= q {
				return kind
			}
		}
		return -1
	}

	// The grammar's processId(): an id ends at an inline `%%`, and a text that starts with one is dropped.
	emitId := func(s, e int) bool {
		at := nextPercent(s)
		if at == -1 || at+2 > e {
			emit(T_ID, s, e)
			return true
		}
		if at == s {
			p = e
			return false
		}
		emit(T_ID, s, at)
		return true
	}

	finish := func(typ int) Tokens {
		toks.add(typ, "", p)
		toks.error = errorText
		return *toks
	}

	for {
		if p >= n {
			// Jison tries the current state's rules once more on the empty remainder.
			switch state {
			case S_INITIAL:
				emit(T_NL, n, n)
			case S_acc_title:
				emit(T_acc_title_value, n, n)
			case S_acc_descr:
				emit(T_acc_descr_value, n, n)
			case S_acc_descr_multiline:
				emit(T_acc_descr_multiline_value, n, n)
			case S_CLASSDEFID:
				emit(T_CLASSDEF_STYLEOPTS, n, n)
			case S_CLASS_STYLE:
				emit(T_STYLECLASS, n, n)
			case S_STYLEDEF_STYLES:
				emit(T_STYLEDEF_STYLEOPTS, n, n)
			case S_STATE_STRING:
				emit(T_STATE_DESCR, n, n)
			case S_FLOATING_NOTE:
				emit(T_NOTE_TEXT, n, n)
			case S_STATE_ID, S_FLOATING_NOTE_ID:
				emit(T_ID, n, n)
			}
			return finish(T_END)
		}

		c := code(p)
		switch state {
		case S_INITIAL, S_struct:
			root := state == S_INITIAL
			lc := c | 32
			if root {
				if lc == 99 && word("click", p) && !isWord(code(p+5)) {
					emit(T_CLICK, p, p+5)
					continue
				}
				if lc == 104 && word("href", p) && !isWord(code(p+4)) {
					emit(T_HREF, p, p+4)
					continue
				}
				if c == 34 {
					close := indexFrom(src, `"`, p+1)
					if close != -1 {
						emit(T_STRING, p, close+1)
						continue
					}
				}
				if lc == 100 && word("default", p) && !isWord(code(p+7)) {
					emit(T_DEFAULT, p, p+7)
					continue
				}
				kind := direction(p)
				if kind != -1 {
					emit(T_direction_tb+kind, p, eol(dirEnd[kind]))
					continue
				}
				if c == 10 {
					j := p + 1
					for code(j) == 10 {
						j++
					}
					emit(T_NL, p, j)
					continue
				}
				if isSpace(c) {
					p = wsEnd(p)
					continue
				}
			} else if c != 10 && isSpace(c) {
				j := p + size(p)
				for j < n && code(j) != 10 && isSpace(code(j)) {
					j += size(j)
				}
				p = j
				continue
			}
			if c == 35 || (c == 37 && code(p+1) == 37 && code(p+2) != 123) {
				p = eol(p)
				continue
			}
			if lc == 115 {
				if root && word("scale", p) {
					e := wsEnd(p + 5)
					if e > p+5 {
						emit(T_scale, p, e)
						push(S_SCALE)
						continue
					}
				}
				if word("style", p) {
					e := wsEnd(p + 5)
					if e > p+5 {
						emit(T_style, p, e)
						push(S_STYLE)
						continue
					}
				}
				if word("state", p) {
					e := wsEnd(p + 5)
					if e > p+5 {
						p = e
						push(S_STATE)
						continue
					}
					if root && word("diagram", p+5) {
						from := p + 12
						if word("-v2", p+12) {
							from = p + 15
						}
						e = wsEnd(from)
						if e > from {
							emit(T_SD, p, e)
							continue
						}
					}
				}
			} else if lc == 99 {
				if word("class", p) {
					def := word("def", p+5)
					from := p + 5
					if def {
						from = p + 8
					}
					e := wsEnd(from)
					if e > from {
						if def {
							emit(T_classDef, p, e)
							push(S_CLASSDEF)
						} else {
							emit(T_class, p, e)
							push(S_CLASS)
						}
						continue
					}
				}
			} else if lc == 97 && root {
				if word("acctitle", p) || word("accdescr", p) {
					title := code(p+3)|32 == 116
					q := wsEnd(p + 8)
					d := code(q)
					if d == 58 {
						if title {
							emit(T_acc_title, p, wsEnd(q+1))
							push(S_acc_title)
						} else {
							emit(T_acc_descr, p, wsEnd(q+1))
							push(S_acc_descr)
						}
						continue
					}
					if d == 123 && !title {
						p = wsEnd(q + 1)
						push(S_acc_descr_multiline)
						continue
					}
				}
			}
			if root {
				if c == 123 {
					emit(T_STRUCT_START, p, p+1)
					push(S_struct)
					continue
				}
			} else {
				kind := direction(p)
				if kind != -1 {
					emit(T_direction_tb+kind, p, eol(dirEnd[kind]))
					continue
				}
				if c == 125 {
					emit(T_STRUCT_STOP, p, p+1)
					pop()
					continue
				}
				if c == 10 {
					p++
					continue
				}
			}
			if lc == 110 && word("note", p) {
				e := wsEnd(p + 4)
				if e > p+4 {
					emit(T_note, p, e)
					push(S_NOTE)
					continue
				}
			}
			if root && lc == 104 && word("hide empty description", p) && !isWord(code(p+22)) {
				emit(T_HIDE_EMPTY, p, p+22)
				continue
			}
			if c == 91 && code(p+1) == 42 && code(p+2) == 93 {
				emit(T_EDGE_STATE, p, p+3)
				continue
			}
			j := p
			for j < n {
				d := code(j)
				if d == 58 || d == 45 || d == 123 || isSpace(d) {
					break
				}
				j += size(j)
			}
			if j > p {
				emitId(p, j)
				continue
			}
			if c == 58 {
				j = p + 1
				for j < n {
					d := code(j)
					if d == 10 || d == 59 {
						break
					}
					if d == 58 {
						after := code(j + 1)
						if j+1 >= n || after == 58 || after == 10 || after == 59 {
							break
						}
						j++
					}
					j += size(j)
				}
				if j > p+1 {
					emitText(T_DESCR, js.Trim(src[p:j]), p, j)
					continue
				}
				if code(p+1) == 58 && code(p+2) == 58 {
					emit(T_STYLE_SEPARATOR, p, p+3)
					continue
				}
			} else if c == 45 && code(p+1) == 45 {
				if code(p+2) == 62 {
					emit(T_ARROW, p, p+3)
					continue
				}
				if !root {
					emit(T_CONCURRENT, p, p+2)
					continue
				}
			}
			if !root {
				return finish(T_ERROR)
			}
			emit(T_INVALID, p, p+size(p))
			continue

		case S_STATE:
			if c != 10 && isSpace(c) {
				j := p + size(p)
				for j < n && code(j) != 10 && isSpace(code(j)) {
					j += size(j)
				}
				p = j
				continue
			}
			if c == 35 || (c == 37 && code(p+1) == 37 && code(p+2) != 123) {
				p = eol(p)
				continue
			}
			kind := stereotype(p)
			if kind != -1 {
				at := stereoAt[kind]
				choice := kind == 2 || kind == 5
				typ := T_JOIN
				length := 8
				if choice {
					typ = T_CHOICE
					length = 10
				} else if kind%3 == 0 {
					typ = T_FORK
				}
				emitText(typ, js.Trim(src[p:at]), p, at+length)
				pop()
				continue
			}
			if c == 34 {
				p++
				push(S_STATE_STRING)
				continue
			}
			q := p
			if c == 10 {
				q = wsEnd(p)
			}
			if word("as", q) {
				e := wsEnd(q + 2)
				if e > q+2 {
					emit(T_AS, p, e)
					push(S_STATE_ID)
					continue
				}
			}
			if c == 10 {
				p++
				pop()
				continue
			}
			if c == 123 {
				emit(T_STRUCT_START, p, p+1)
				pop()
				push(S_struct)
				continue
			}
			if isWord(c) {
				first := wordEnd(p)
				second := wsEnd(first)
				if second > first && isWord(code(second)) {
					brace := nextBrace(second + 1)
					if brace != -1 && brace < lineEnd(second) {
						errorText = `Error: State name must be a single word. Found: "` + js.Trim(src[p:brace+1]) + `"`
						return finish(T_ERROR)
					}
				}
			}
			j := p + size(p)
			for j < n {
				d := code(j)
				if d == 123 || isSpace(d) {
					break
				}
				j += size(j)
			}
			emit(T_COMPOSIT_STATE, p, j)
			continue

		case S_STATE_STRING:
			if c == 34 {
				p++
				pop()
				continue
			}
			close := indexFrom(src, `"`, p)
			if close == -1 {
				close = n
			}
			emit(T_STATE_DESCR, p, close)
			continue

		case S_STATE_ID:
			j := p
			for j < n {
				d := src[j]
				if d == 10 || d == 123 {
					break
				}
				j++
			}
			if emitId(p, j) {
				pop()
			}
			continue

		case S_SCALE:
			if c >= 48 && c <= 57 {
				j := p + 1
				for j < n && src[j] >= 48 && src[j] <= 57 {
					j++
				}
				emit(T_WIDTH, p, j)
				continue
			}
			q := wsEnd(p)
			if q > p && word("width", q) && !isWord(code(q+5)) {
				p = q + 5
				pop()
				continue
			}
			return finish(T_ERROR)

		case S_acc_title:
			emit(T_acc_title_value, p, eol(p))
			pop()
			continue

		case S_acc_descr:
			emit(T_acc_descr_value, p, eol(p))
			pop()
			continue

		case S_acc_descr_multiline:
			if c == 125 {
				p++
				pop()
				continue
			}
			close := indexFrom(src, "}", p)
			if close == -1 {
				close = n
			}
			emit(T_acc_descr_multiline_value, p, close)
			continue

		case S_CLASSDEF:
			isDefault := word("default", p) && wsEnd(p+7) > p+7
			j := wordEnd(p)
			if isDefault {
				j = p + 7
			}
			e := wsEnd(j)
			if j == p || e == j {
				return finish(T_ERROR)
			}
			if isDefault {
				emit(T_DEFAULT_CLASSDEF_ID, p, e)
			} else {
				emit(T_CLASSDEF_ID, p, e)
			}
			pop()
			push(S_CLASSDEFID)
			continue

		case S_CLASSDEFID:
			emit(T_CLASSDEF_STYLEOPTS, p, eol(p))
			pop()
			continue

		case S_CLASS:
			j := wordEnd(p)
			if j == p {
				return finish(T_ERROR)
			}
			for code(j) == 44 {
				k := wsEnd(j + 1)
				e := wordEnd(k)
				if e == k {
					break
				}
				j = e
			}
			emit(T_CLASSENTITY_IDS, p, j)
			pop()
			push(S_CLASS_STYLE)
			continue

		case S_CLASS_STYLE:
			emit(T_STYLECLASS, p, eol(p))
			pop()
			continue

		case S_STYLE:
			j := p
			for j < n && (isWord(code(j)) || src[j] == 44) {
				j++
			}
			e := wsEnd(j)
			if j == p || e == j {
				return finish(T_ERROR)
			}
			emit(T_STYLE_IDS, p, e)
			pop()
			push(S_STYLEDEF_STYLES)
			continue

		case S_STYLEDEF_STYLES:
			emit(T_STYLEDEF_STYLEOPTS, p, eol(p))
			pop()
			continue

		case S_NOTE:
			if word("left of", p) && !isWord(code(p+7)) {
				emit(T_left_of, p, p+7)
				pop()
				push(S_NOTE_ID)
				continue
			}
			if word("right of", p) && !isWord(code(p+8)) {
				emit(T_right_of, p, p+8)
				pop()
				push(S_NOTE_ID)
				continue
			}
			if c == 34 {
				p++
				pop()
				push(S_FLOATING_NOTE)
				continue
			}
			return finish(T_ERROR)

		case S_NOTE_ID:
			q := wsEnd(p)
			j := q
			for j < n {
				d := code(j)
				if d == 58 || d == 45 || isSpace(d) {
					break
				}
				j += size(j)
			}
			if j == q {
				return finish(T_ERROR)
			}
			if emitId(p, j) {
				pop()
				push(S_NOTE_TEXT)
			}
			continue

		case S_NOTE_TEXT:
			q := wsEnd(p)
			if code(q) == 58 {
				j := q + 1
				for j < n {
					d := code(j)
					if d == 58 || d == 10 || d == 59 {
						break
					}
					j += size(j)
				}
				if j > q+1 {
					// Mermaid drops two characters here, expecting " :"; with no space before the colon
					// the first character of the note goes too.
					from := p + size(p)
					from += size(from)
					text := ""
					if from < j {
						text = js.Trim(src[from:j])
					}
					emitText(T_NOTE_TEXT, text, p, j)
					pop()
					continue
				}
			}
			k := indexFrom(src, "\n", p)
			e := -1
			for k != -1 {
				e = wsEnd(k)
				if word("end note", e) && !isWord(code(e+8)) {
					break
				}
				k = indexFrom(src, "\n", e)
			}
			if k == -1 {
				return finish(T_ERROR)
			}
			emitText(T_NOTE_TEXT, js.Trim(src[p:e]), p, e+8)
			pop()
			continue

		case S_FLOATING_NOTE:
			q := wsEnd(p)
			if word("as", q) {
				emit(T_AS, p, wsEnd(q+2))
				pop()
				push(S_FLOATING_NOTE_ID)
				continue
			}
			if c == 34 {
				p++
				continue
			}
			close := indexFrom(src, `"`, p)
			if close == -1 {
				close = n
			}
			emit(T_NOTE_TEXT, p, close)
			continue

		case S_FLOATING_NOTE_ID:
			if emitId(p, eol(p)) {
				pop()
			}
			continue
		}
	}
}
