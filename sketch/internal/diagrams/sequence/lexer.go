// Ported from pele src/diagrams/sequence/lexer.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// As in the flowchart lexer, the port reads UTF-8 bytes: code() decodes the
// character at a byte offset, and a step past a character that may not be
// ASCII steps the whole character.

package sequence

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

// Lexer states.
const (
	S_INITIAL = iota
	S_ID
	S_ALIAS
	S_LINE
	S_CONFIG
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
)

// Character classes.
const (
	C_ActorStart = 1
	C_ActorRest  = 2
	C_Id         = 4
	C_IdWide     = 8
	C_Line       = 16
	C_Menu       = 32
)

// CLASS holds the character classes of Mermaid's lexer rules, for ASCII. Other characters are tested by isSpace.
var CLASS = func() [128]uint8 {
	var t [128]uint8
	for c := 0; c < 128; c++ {
		ch := string(rune(c))
		ws := c == 32 || (c >= 9 && c <= 13)
		bits := uint8(0)
		if !strings.Contains("/\\+()<->:\n,;", ch) {
			bits |= C_ActorStart
		}
		if !strings.Contains("+<->:\n,;", ch) {
			bits |= C_ActorRest
		}
		if !strings.Contains("<>:\n,;@", ch) {
			bits |= C_IdWide
			if !ws {
				bits |= C_Id
			}
		}
		if !strings.Contains("#\n;", ch) {
			bits |= C_Line
		}
		if !strings.Contains("/\\+()<>:\n,;-", ch) && !ws {
			bits |= C_Menu
		}
		t[c] = bits
	}
	return t
}()

func tokenize(src string) Tokens {
	n := len(src)
	var types []int
	var texts []string
	var starts []int
	var stack []int
	state := S_INITIAL
	p := 0
	nextNl := -1

	code := func(q int) rune {
		if q < 0 || q >= n {
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
	size := func(q int) int {
		if q >= n || src[q] < utf8.RuneSelf {
			return 1
		}
		_, s := utf8.DecodeRuneInString(src[q:])
		return s
	}
	isSpace := func(c rune) bool { return util.IsSpace(c) }

	emit := func(typ int, s, e int) {
		types = append(types, typ)
		texts = append(texts, src[s:e])
		starts = append(starts, s)
		p = e
	}
	emitText := func(typ int, s, e int, text string) {
		types = append(types, typ)
		texts = append(texts, text)
		starts = append(starts, s)
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
	eol := func(q int) int {
		if nextNl < q {
			nextNl = indexFrom(src, "\n", q)
			if nextNl == -1 {
				nextNl = n
			}
		}
		return nextNl
	}
	has := func(q int, bit uint8) bool {
		c := code(q)
		if c >= 0 && c < 128 {
			return CLASS[c]&bit != 0
		}
		if bit == C_Id || bit == C_Menu {
			return !isSpace(c)
		}
		return true
	}
	// ci matches a lowercase literal without regard to the case of the source.
	ci := func(q int, word string) bool {
		for k := 0; k < len(word); k++ {
			w := rune(word[k])
			c := code(q + k)
			if w >= 97 && w <= 122 {
				if c|32 != w {
					return false
				}
			} else if c != w {
				return false
			}
		}
		return true
	}
	word := func(lit string) bool { return ci(p, lit) && !util.IsWord(code(p+len(lit))) }
	skipWs := func(q int) int {
		for q < n && isSpace(code(q)) {
			q += size(q)
		}
		return q
	}
	skipSameLine := func() bool {
		q := p
		for q < n {
			c := code(q)
			if c == 10 || !isSpace(c) {
				break
			}
			q += size(q)
		}
		if q == p {
			return false
		}
		p = q
		return true
	}
	lineRun := func(q int) int {
		for q < n && has(q, C_Line) {
			q++
		}
		return q
	}
	begin := func(typ int, length int, next int) {
		emit(typ, p, p+length)
		push(next)
	}
	keyword := func(lit string, typ int, next int) bool {
		if !word(lit) {
			return false
		}
		emit(typ, p, p+len(lit))
		if next >= 0 {
			push(next)
		}
		return true
	}
	// menu: the actor menu keywords count only when spaces and the start of a name follow.
	menu := func(lit string, typ int) bool {
		if !ci(p, lit) {
			return false
		}
		q := p + len(lit)
		c := code(q)
		if c != 32 && c != 9 {
			return false
		}
		for c == 32 || c == 9 {
			q++
			c = code(q)
		}
		if q >= n || !has(q, C_Menu) {
			return false
		}
		emit(typ, p, p+len(lit))
		return true
	}

	arrowAhead := func(q int) bool {
		c := code(q)
		d := code(q + 1)
		if c == 45 {
			if d == 120 || d == 88 || d == 41 || d == 92 || d == 47 || d == 45 {
				return true
			}
			e := code(q + 2)
			return d == 124 && (e == 92 || e == 47)
		}
		if c == 47 || c == 92 {
			return (d == 124 || d == c) && code(q+2) == 45
		}
		return c == 40 && d == 41
	}

	actor := func() {
		q := p + size(p)
		for q < n && has(q, C_ActorStart) {
			q++
		}
		for q < n && !arrowAhead(q) {
			r := q
			for code(r) == 45 {
				r++
			}
			if r >= n || !has(r, C_ActorRest) {
				break
			}
			r++
			for r < n && has(r, C_ActorRest) {
				r++
			}
			q = r
		}
		emitText(T_ACTOR, p, q, js.Trim(src[p:q]))
	}

	isDigit := func(c rune) bool { return util.IsDigit(c) }

	number := func() bool {
		q := p
		for isDigit(code(q)) {
			q++
		}
		whole := q > p
		if code(q) == 46 && isDigit(code(q+1)) {
			r := q + 2
			if isDigit(code(q + 2)) {
				r = q + 3
			}
			after := code(r)
			if after == 32 || after == 10 {
				emit(T_NUM, p, r)
				return true
			}
		}
		after := code(q)
		if whole && (after == 32 || after == 10) {
			emit(T_NUM, p, q)
			return true
		}
		return false
	}

	dash := func() {
		d := code(p + 1)
		e := code(p + 2)
		switch {
		case d == 62:
			if e == 62 {
				emit(T_SOLID_ARROW, p, p+3)
			} else {
				emit(T_SOLID_OPEN_ARROW, p, p+2)
			}
		case d == 120 || d == 88:
			emit(T_SOLID_CROSS, p, p+2)
		case d == 41:
			emit(T_SOLID_POINT, p, p+2)
		case d == 124 && e == 92:
			emit(T_SOLID_ARROW_TOP, p, p+3)
		case d == 124 && e == 47:
			emit(T_SOLID_ARROW_BOTTOM, p, p+3)
		case d == 92 && e == 92:
			emit(T_STICK_ARROW_TOP, p, p+3)
		case d == 47 && e == 47:
			emit(T_STICK_ARROW_BOTTOM, p, p+3)
		case d == 45:
			f := code(p + 3)
			switch {
			case e == 62:
				if f == 62 {
					emit(T_DOTTED_ARROW, p, p+4)
				} else {
					emit(T_DOTTED_OPEN_ARROW, p, p+3)
				}
			case e == 120 || e == 88:
				emit(T_DOTTED_CROSS, p, p+3)
			case e == 41:
				emit(T_DOTTED_POINT, p, p+3)
			case e == 124 && f == 92:
				emit(T_SOLID_ARROW_TOP_DOTTED, p, p+4)
			case e == 124 && f == 47:
				emit(T_SOLID_ARROW_BOTTOM_DOTTED, p, p+4)
			case e == 92 && f == 92:
				emit(T_STICK_ARROW_TOP_DOTTED, p, p+4)
			case e == 47 && f == 47:
				emit(T_STICK_ARROW_BOTTOM_DOTTED, p, p+4)
			default:
				emit(T_MINUS, p, p+1)
			}
		default:
			emit(T_MINUS, p, p+1)
		}
	}

	reverse := func(c rune) bool {
		d := code(p + 1)
		if (d != 124 && d != c) || code(p+2) != 45 {
			return false
		}
		dotted := code(p+3) == 45
		top := c == 47
		var typ int
		if d == 124 {
			if dotted {
				typ = T_SOLID_ARROW_BOTTOM_REVERSE_DOTTED
				if top {
					typ = T_SOLID_ARROW_TOP_REVERSE_DOTTED
				}
			} else {
				typ = T_SOLID_ARROW_BOTTOM_REVERSE
				if top {
					typ = T_SOLID_ARROW_TOP_REVERSE
				}
			}
		} else if dotted {
			typ = T_STICK_ARROW_BOTTOM_REVERSE_DOTTED
			if top {
				typ = T_STICK_ARROW_TOP_REVERSE_DOTTED
			}
		} else {
			typ = T_STICK_ARROW_BOTTOM_REVERSE
			if top {
				typ = T_STICK_ARROW_TOP_REVERSE
			}
		}
		end := p + 3
		if dotted {
			end = p + 4
		}
		emit(typ, p, end)
		return true
	}

	title := func() bool {
		if !ci(p, "title") {
			return false
		}
		q := p + 5
		legacy := code(q) == 58
		if legacy {
			q++
		}
		if !isSpace(code(q)) {
			return false
		}
		next := q + size(q)
		if next >= n || !has(next, C_Line) {
			return false
		}
		typ := T_title
		if legacy {
			typ = T_legacy_title
		}
		emit(typ, p, lineRun(next))
		return true
	}

	acc := func() bool {
		descr := ci(p, "accdescr")
		if !descr && !ci(p, "acctitle") {
			return false
		}
		q := skipWs(p + 8)
		c := code(q)
		if c == 58 {
			typ, st := T_acc_title, S_acc_title
			if descr {
				typ, st = T_acc_descr, S_acc_descr
			}
			emit(typ, p, skipWs(q+1))
			push(st)
			return true
		}
		if descr && c == 123 {
			p = skipWs(q + 1)
			push(S_acc_descr_multiline)
			return true
		}
		return false
	}

	keywords := func(c rune) bool {
		switch c | 32 {
		case 97:
			return keyword("actor", T_participant_actor, S_ID) ||
				keyword("alt", T_alt, S_LINE) ||
				keyword("and", T_and, S_LINE) ||
				keyword("activate", T_activate, S_ID) ||
				acc() ||
				keyword("autonumber", T_autonumber, -1)
		case 98:
			return keyword("box", T_box, S_LINE) || keyword("break", T_break, S_LINE)
		case 99:
			return keyword("create", T_create, -1) || keyword("critical", T_critical, S_LINE)
		case 100:
			return keyword("destroy", T_destroy, S_ID) || menu("details", T_details) || keyword("deactivate", T_deactivate, S_ID)
		case 101:
			return keyword("else", T_else, S_LINE) || keyword("end", T_end, -1)
		case 108:
			return keyword("loop", T_loop, S_LINE) || keyword("left of", T_left_of, -1) || menu("links", T_links) || menu("link", T_link)
		case 110:
			return keyword("note", T_note, -1)
		case 111:
			return keyword("opt", T_opt, S_LINE) || keyword("option", T_option, S_LINE) || keyword("over", T_over, -1) || keyword("off", T_off, -1)
		case 112:
			return keyword("participant", T_participant, S_ID) ||
				keyword("par", T_par, S_LINE) ||
				keyword("par_over", T_par_over, S_LINE) ||
				menu("properties", T_properties)
		case 114:
			return keyword("rect", T_rect, S_LINE) || keyword("right of", T_right_of, -1)
		case 115:
			return keyword("sequencediagram", T_SD, -1)
		case 116:
			return title()
		}
		return false
	}

	initial := func() {
		c := code(p)
		if c == 10 {
			q := p + 1
			for code(q) == 10 {
				q++
			}
			emit(T_NEWLINE, p, q)
			return
		}
		if isSpace(c) {
			p = skipWs(p + size(p))
			return
		}
		if c == 35 || (c == 37 && code(p+1) != 123) {
			p = eol(p)
			return
		}
		if c != 125 && code(p+1) == 37 && code(p+2) == 37 {
			p = eol(p)
			return
		}
		if (isDigit(c) || c == 46) && number() {
			return
		}
		if c >= 0 && c < 128 && (c|32) >= 97 && (c|32) <= 122 && keywords(c) {
			return
		}
		switch {
		case c == 44:
			emit(T_COMMA, p, p+1)
		case c == 59:
			emit(T_NEWLINE, p, p+1)
		case has(p, C_ActorStart):
			actor()
		case c == 45:
			dash()
		case c == 60 && strings.HasPrefix(src[p:], "<<->>"):
			emit(T_BIDIRECTIONAL_SOLID_ARROW, p, p+5)
		case c == 60 && strings.HasPrefix(src[p:], "<<-->>"):
			emit(T_BIDIRECTIONAL_DOTTED_ARROW, p, p+6)
		case (c == 47 || c == 92) && reverse(c):
			return
		case c == 58:
			emit(T_TXT, p, lineRun(p+1))
		case c == 43:
			emit(T_PLUS, p, p+1)
		case c == 40 && code(p+1) == 41:
			emit(T_CENTRAL, p, p+2)
		default:
			emit(T_INVALID, p, p+size(p))
		}
	}

	// id: a participant name, up to an alias, a config object, or the end of the statement.
	id := func() bool {
		if skipSameLine() {
			return true
		}
		c := code(p)
		if c == 35 {
			p = eol(p)
			return true
		}
		if c == 64 && code(p+1) == 123 {
			begin(T_CONFIG_START, 2, S_CONFIG)
			return true
		}
		a := p
		for a < n && has(a, C_Id) {
			a++
		}
		if a > p {
			w := skipWs(a)
			if code(w) == 64 && code(w+1) == 123 {
				emit(T_ACTOR, p, a)
				return true
			}
			if w > a && ci(w, "as") && isSpace(code(w+2)) {
				emit(T_ACTOR, p, a)
				push(S_ALIAS)
				return true
			}
		}
		b := a
		for b < n && has(b, C_IdWide) {
			b++
		}
		if b > p {
			end := code(b)
			// Before any other delimiter the name must be followed by a comment, so it ends at the last one.
			cut := b
			if b < n && end != 10 && end != 59 {
				cut--
				for cut > p && code(cut) != 35 {
					cut--
				}
			}
			if cut > p {
				emitText(T_ACTOR, p, cut, js.Trim(src[p:cut]))
				pop()
				return true
			}
		}
		if code(b) == 60 {
			emit(T_INVALID, p, eol(p))
			pop()
			return true
		}
		if c != 10 {
			e := eol(p)
			emitText(T_INVALID, p, e, js.Trim(src[p:e]))
			pop()
			return true
		}
		return false
	}

	step := func() bool {
		switch state {
		case S_INITIAL:
			initial()
			return true
		case S_ID:
			return id()
		case S_ALIAS:
			if skipSameLine() {
				return true
			}
			if code(p) == 35 {
				p = eol(p)
				return true
			}
			pop()
			pop()
			if word("as") {
				begin(T_AS, 2, S_LINE)
			} else {
				emit(T_NEWLINE, p, p)
			}
			return true
		case S_LINE:
			if skipSameLine() {
				return true
			}
			if code(p) == 35 {
				p = eol(p)
				return true
			}
			emit(T_restOfLine, p, lineRun(p))
			pop()
			return true
		case S_CONFIG:
			if code(p) != 125 {
				q := indexFrom(src, "}", p)
				if q == -1 {
					q = n
				}
				emit(T_CONFIG_CONTENT, p, q)
				return true
			}
			w := skipWs(p + 1)
			alias := w > p+1 && ci(w, "as") && isSpace(code(w+2))
			emit(T_CONFIG_END, p, p+1)
			pop()
			if alias {
				push(S_ALIAS)
			} else {
				pop()
			}
			return true
		case S_acc_title, S_acc_descr:
			typ := T_acc_descr_value
			if state == S_acc_title {
				typ = T_acc_title_value
			}
			emit(typ, p, eol(p))
			pop()
			return true
		}
		if code(p) == 125 {
			p++
			pop()
		} else {
			q := indexFrom(src, "}", p)
			if q == -1 {
				q = n
			}
			emit(T_acc_descr_multiline_value, p, q)
		}
		return true
	}

	for p < n {
		if !step() {
			types = append(types, T_ERROR)
			texts = append(texts, "")
			starts = append(starts, p)
			return Tokens{types, texts, starts}
		}
	}

	// Mermaid's lexer tries its rules once more on the empty remainder before reporting the end.
	switch state {
	case S_INITIAL, S_ALIAS:
		emit(T_NEWLINE, n, n)
	case S_LINE:
		emit(T_restOfLine, n, n)
	case S_acc_title:
		emit(T_acc_title_value, n, n)
	case S_acc_descr:
		emit(T_acc_descr_value, n, n)
	case S_acc_descr_multiline:
		emit(T_acc_descr_multiline_value, n, n)
	}
	types = append(types, T_END)
	texts = append(texts, "")
	starts = append(starts, n)
	return Tokens{types, texts, starts}
}

// indexFrom is s.indexOf(sub, from).
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
