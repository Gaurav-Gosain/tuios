// Ported from pele src/diagrams/er/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// As in the other lexers, the port reads UTF-8 bytes: code() decodes the character at a
// byte offset, and a step past a character that may not be ASCII steps the whole
// character. pele's closures are methods of a lexer here. The sticky regular expressions
// are written out as hand matchers.

package er

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

// Lexer states.
const (
	S_INITIAL = iota
	S_block
	S_block_bq
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
	S_style
)

const (
	TEXT       = 1
	STYLE_TEXT = 2
	ATTR_START = 4
	ATTR_REST  = 8
)

var CLASS = func() (class [128]uint8) {
	for c := 0; c < 128; c++ {
		word := (c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95
		letter := word && (c < 48 || c > 57)
		if word {
			class[c] |= TEXT | STYLE_TEXT | ATTR_REST
		}
		if letter {
			class[c] |= ATTR_START
		}
	}
	for _, ch := range "-*" {
		class[ch] |= TEXT | STYLE_TEXT | ATTR_REST
	}
	class[46] |= TEXT | ATTR_REST
	class[42] |= ATTR_START
	for _, ch := range "[]()," {
		class[ch] |= ATTR_REST
	}
	return
}()

var directionNames = [4]string{"tb", "bt", "rl", "lr"}

// Mermaid's attribute pattern is case-insensitive, which lets the micro sign into its À-￿ range.
func isAttr(c rune, kind uint8) bool {
	if c < 0 {
		return false
	}
	if c < 128 {
		return CLASS[c]&kind != 0
	}
	return c >= 0xc0 || c == 0xb5
}

// inClass is c >= 128 || (CLASS[c] & kind) !== 0 for a byte, which is false past the end.
func inClass(src string, q int, kind uint8) bool {
	if q >= len(src) {
		return false
	}
	c := src[q]
	return c >= 128 || CLASS[c]&kind != 0
}

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

// indexFold finds a lower-case ASCII word from a byte offset, ignoring case.
func indexFold(s, word string, from int) int {
	for q := max(from, 0); q+len(word) <= len(s); q++ {
		if hasFold(s, q, word) {
			return q
		}
	}
	return -1
}

// hasFold is whether a lower-case ASCII word is at q, ignoring case.
func hasFold(s string, q int, word string) bool {
	if q < 0 || q+len(word) > len(s) {
		return false
	}
	for i := 0; i < len(word); i++ {
		k := word[i]
		c := s[q+i]
		if c != k && (k < 97 || k > 122 || c|32 != k) {
			return false
		}
	}
	return true
}

// codeAt decodes the character at a byte offset: -1 past the end, 0xfffe for a broken
// sequence, and 0xd800 for a character outside the BMP, which pele reads as a surrogate.
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

type lexer struct {
	src   string
	n     int
	toks  Tokens
	state int
	p     int

	dirNext   int
	dirFailed int
	lineEnd   int
	tildeNext int
	tildeLine int
	tildeLast int
	wsNext    int
}

func (l *lexer) code(q int) rune { return codeAt(l.src, q) }

//go:noinline
func (l *lexer) emit(typ int, s, e int) bool {
	l.toks.types = append(l.toks.types, typ)
	l.toks.texts = append(l.toks.texts, l.src[s:e])
	l.toks.starts = append(l.toks.starts, s)
	l.p = e
	return true
}

// Matches a lower-case keyword at p, ignoring case.
func (l *lexer) kw(word string) bool { return hasFold(l.src, l.p, word) }

//go:noinline
func (l *lexer) kwb(word string) bool {
	return l.kw(word) && !util.IsWord(l.code(l.p+len(word)))
}

func (l *lexer) lower(q int) rune {
	c := l.code(q)
	if c >= 65 && c <= 90 {
		return c + 32
	}
	return c
}

func (l *lexer) nextLineEnd(q int) int {
	if e := reLineEnd(l.src, q); e != -1 {
		return e
	}
	return l.n
}

func (l *lexer) digits(q int) int {
	for q < l.n && util.IsDigit(rune(l.src[q])) {
		q++
	}
	return q
}

//go:noinline
func (l *lexer) skipWs(q int) int {
	for q < l.n && util.IsSpace(l.code(q)) {
		q += sizeAt(l.src, q)
	}
	return q
}

//go:noinline
func (l *lexer) until(ch string, from int) int {
	e := indexFrom(l.src, ch, from)
	if e == -1 {
		return l.n
	}
	return e
}

// accAt matches /accTitle\s*:\s*/iy and its siblings at p: a word, spaces, the mark and
// spaces. Returns the end of the match, or -1.
func (l *lexer) accAt(word string, mark byte) int {
	if !l.kw(word) {
		return -1
	}
	q := l.skipWs(l.p + len(word))
	if q >= l.n || l.src[q] != mark {
		return -1
	}
	return l.skipWs(q + 1)
}

// RE_DIRECTION[kind] at p: /.*direction\s+TB[^\n]*/iy and its siblings. The greedy `.*` takes
// the last `direction` on the line that is followed by the direction, and the match runs to
// the line feed after it. Returns the end of the match, or -1.
func (l *lexer) matchDirection(kind int, le int) int {
	src := l.src
	name := directionNames[kind]
	end := -1
	for d := indexFold(src, "direction", l.p); d != -1 && d < le; d = indexFold(src, "direction", d+1) {
		w := l.skipWs(d + 9)
		if w == d+9 || !hasFold(src, w, name) {
			continue
		}
		end = l.until("\n", w+2)
	}
	return end
}

// A direction statement swallows its whole line, from wherever the lexer stands on it.
func (l *lexer) direction() bool {
	if l.dirNext == -1 {
		return false
	}
	if l.dirNext < l.p {
		l.dirNext = indexFold(l.src, "direction", l.p)
		if l.dirNext == -1 {
			return false
		}
	}
	if l.p < l.dirFailed {
		return false
	}
	if l.lineEnd < l.p {
		l.lineEnd = l.nextLineEnd(l.p)
	}
	if l.dirNext >= l.lineEnd {
		return false
	}
	for i := 0; i < 4; i++ {
		if e := l.matchDirection(i, l.lineEnd); e >= 0 {
			return l.emit(T_direction_tb+i, l.p, e)
		}
	}
	l.dirFailed = l.lineEnd
	return false
}

func (l *lexer) quoted() {
	p := l.p
	e := indexFrom(l.src, `"`, p+1)
	if e == -1 {
		l.emit(T_CHAR, p, p+1)
		return
	}
	name := e > p+1
	for q := p + 1; name && q < e; q++ {
		c := l.src[q]
		if c == 37 || c == 92 || c == 13 || c == 10 || c == 11 || c == 8 {
			name = false
		}
	}
	if name {
		l.emit(T_ENTITY_NAME, p, e+1)
	} else {
		l.emit(T_WORD, p, e+1)
	}
}

func (l *lexer) number(c rune) {
	p := l.p
	d := l.code(p + 1)
	if d == 43 && (c == 49 || c == 48) {
		if c == 49 {
			l.emit(T_ONE_OR_MORE, p, p+2)
		} else {
			l.emit(T_ZERO_OR_MORE, p, p+2)
		}
		return
	}
	q := l.digits(p + 1)
	if l.code(q) == 46 && util.IsDigit(l.code(q+1)) {
		l.emit(T_DECIMAL_NUM, p, l.digits(q+2))
		return
	}
	if c == 49 {
		if util.IsSpace(d) {
			r := l.skipWs(p + 1)
			f := l.code(r)
			if util.IsWord(f) || f == 34 || f == 39 {
				l.emit(T_ONLY_ONE, p, p+1)
				return
			}
		} else if d == 45 || d == 46 {
			f := l.code(p + 2)
			if f == 45 || f == 46 {
				l.emit(T_ONLY_ONE, p, p+1)
				return
			}
		}
		if !util.IsWord(d) {
			l.emit(T_ENTITY_ONE, p, p+1)
			return
		}
	}
	l.emit(T_NUM, p, q)
}

func (l *lexer) keyword(lc rune) bool {
	p := l.p
	d := l.code(p + 1)
	switch lc {
	case 101:
		if l.kwb("erdiagram") {
			return l.emit(T_ER_DIAGRAM, p, p+9)
		}
		if l.kwb("end") {
			return l.emit(T_END_KW, p, l.skipWs(p+3))
		}
	case 115:
		if l.kwb("style") {
			l.state = S_style
			return l.emit(T_STYLE, p, p+5)
		}
		if l.kwb("subgraph") {
			return l.emit(T_SUBGRAPH, p, p+8)
		}
	case 99:
		if l.kwb("classdef") {
			l.state = S_style
			return l.emit(T_CLASSDEF, p, p+8)
		}
		if l.kwb("class") {
			return l.emit(T_CLASS, p, p+5)
		}
	case 111:
		if l.kwb("one or zero") {
			return l.emit(T_ZERO_OR_ONE, p, p+11)
		}
		if l.kwb("one or more") || l.kwb("one or many") {
			return l.emit(T_ONE_OR_MORE, p, p+11)
		}
		if l.kwb("one") {
			return l.emit(T_ONLY_ONE, p, p+3)
		}
		if l.kwb("only one") {
			return l.emit(T_ONLY_ONE, p, p+8)
		}
		if d == 124 {
			return l.emit(T_ZERO_OR_ONE, p, p+2)
		}
		if d == 123 {
			return l.emit(T_ZERO_OR_MORE, p, p+2)
		}
		if l.kwb("optionally to") {
			return l.emit(T_NON_IDENTIFYING, p, p+13)
		}
	case 122:
		if l.kwb("zero or one") {
			return l.emit(T_ZERO_OR_ONE, p, p+11)
		}
		if l.kwb("zero or more") || l.kwb("zero or many") {
			return l.emit(T_ZERO_OR_MORE, p, p+12)
		}
	case 109:
		if l.kw("many(0)") {
			return l.emit(T_ZERO_OR_MORE, p, p+7)
		}
		if l.kw("many(1)") {
			return l.emit(T_ONE_OR_MORE, p, p+7)
		}
		if l.kwb("many") {
			return l.emit(T_ZERO_OR_MORE, p, p+4)
		}
	case 116:
		if l.kwb("to") {
			return l.emit(T_IDENTIFYING, p, p+2)
		}
	case 117:
		if d == 46 || d == 45 || d == 124 {
			return l.emit(T_MD_PARENT, p, p+1)
		}
	case 124:
		if l.lower(p+1) == 111 && !util.IsWord(l.code(p+2)) {
			return l.emit(T_ZERO_OR_ONE, p, p+2)
		}
		if d == 124 {
			return l.emit(T_ONLY_ONE, p, p+2)
		}
		if d == 123 {
			return l.emit(T_ONE_OR_MORE, p, p+2)
		}
	case 125:
		if l.lower(p+1) == 111 && !util.IsWord(l.code(p+2)) {
			return l.emit(T_ZERO_OR_MORE, p, p+2)
		}
		if d == 124 {
			return l.emit(T_ONE_OR_MORE, p, p+2)
		}
	case 46:
		if d == 46 || d == 45 {
			return l.emit(T_NON_IDENTIFYING, p, p+2)
		}
	case 45:
		if d == 45 {
			return l.emit(T_IDENTIFYING, p, p+2)
		}
		if d == 46 {
			return l.emit(T_NON_IDENTIFYING, p, p+2)
		}
	}
	return false
}

func (l *lexer) initial(c rune) {
	p := l.p
	if (c == 97 || c == 65) && l.kw("acc") {
		if e := l.accAt("acctitle", ':'); e >= 0 {
			l.state = S_acc_title
			l.emit(T_acc_title, p, e)
			return
		}
		if e := l.accAt("accdescr", ':'); e >= 0 {
			l.state = S_acc_descr
			l.emit(T_acc_descr, p, e)
			return
		}
		if e := l.accAt("accdescr", '{'); e >= 0 {
			l.state = S_acc_descr_multiline
			l.p = e
			return
		}
	}
	if l.direction() {
		return
	}

	src := l.src
	switch c {
	case 32, 9, 13:
		e := p + 1
		for e < l.n && (src[e] == 32 || src[e] == 9 || src[e] == 13) {
			e++
		}
		l.p = e
		return
	case 10:
		e := p + 1
		for e < l.n && src[e] == 10 {
			e++
		}
		l.emit(T_NEWLINE, p, e)
		return
	case 34:
		l.quoted()
		return
	case 123:
		l.state = S_block
		l.emit(T_BLOCK_START, p, p+1)
		return
	case 35:
		l.emit(T_BRKT, p, p+1)
		return
	case 44:
		l.emit(T_COMMA, p, p+1)
		return
	case 58:
		if l.code(p+1) == 58 && l.code(p+2) == 58 {
			l.emit(T_STYLE_SEPARATOR, p, p+3)
		} else {
			l.emit(T_COLON, p, p+1)
		}
		return
	case 91:
		l.emit(T_SQS, p, p+1)
		return
	case 93:
		l.emit(T_SQE, p, p+1)
		return
	}
	if util.IsDigit(c) {
		l.number(c)
		return
	}
	lc := c
	if c >= 65 && c <= 90 {
		lc = c + 32
	}
	if l.keyword(lc) {
		return
	}
	if inClass(src, p, TEXT) {
		e := p + 1
		for inClass(src, e, TEXT) {
			e++
		}
		l.emit(T_UNICODE_TEXT, p, e)
		return
	}
	l.emit(T_CHAR, p, p+1)
}

// Mermaid's generic type rule: a run of non-space characters holding a `~`, through the last `~` on the line.
func (l *lexer) generic() bool {
	if l.tildeNext == -1 {
		return false
	}
	p := l.p
	if l.tildeNext < p {
		l.tildeNext = indexFrom(l.src, "~", p)
		if l.tildeNext == -1 {
			return false
		}
	}
	if l.wsNext < p {
		q := p
		for q < l.n && !util.IsSpace(l.code(q)) {
			q += sizeAt(l.src, q)
		}
		l.wsNext = q
	}
	if l.tildeNext >= l.wsNext {
		return false
	}
	if l.tildeLine < p {
		l.tildeLine = l.nextLineEnd(p)
		l.tildeLast = strings.LastIndexByte(l.src[:l.tildeLine], '~')
	}
	if l.tildeLast <= l.tildeNext {
		return false
	}
	e := l.tildeLast + 1
	for e < l.n && !util.IsSpace(l.code(e)) {
		e += sizeAt(l.src, e)
	}
	return l.emit(T_ATTRIBUTE_WORD, p, e)
}

func (l *lexer) block(c rune) {
	p := l.p
	if util.IsSpace(c) {
		l.p = l.skipWs(p)
		return
	}
	lc := c | 32
	if (lc == 112 || lc == 102 || lc == 117) && l.code(p+1)|32 == 107 && !util.IsWord(l.code(p+2)) {
		l.emit(T_ATTRIBUTE_KEY, p, p+2)
		return
	}
	if l.generic() {
		return
	}
	if isAttr(c, ATTR_START) {
		e := p + sizeAt(l.src, p)
		for e < l.n && isAttr(l.code(e), ATTR_REST) {
			e += sizeAt(l.src, e)
		}
		l.emit(T_ATTRIBUTE_WORD, p, e)
		return
	}
	if c == 96 {
		l.state = S_block_bq
		l.p++
		return
	}
	if c == 34 {
		if e := indexFrom(l.src, `"`, p+1); e != -1 {
			l.emit(T_COMMENT, p, e+1)
			return
		}
	} else if c == 125 {
		l.state = S_INITIAL
		l.emit(T_BLOCK_STOP, p, p+1)
		return
	}
	l.emit(T_CHAR, p, p+sizeAt(l.src, p))
}

// step lexes one token, or one skipped stretch, in the current state. It returns false on text
// that no rule of the state matches.
func (l *lexer) step(c rune) bool {
	p := l.p
	switch l.state {
	case S_INITIAL:
		l.initial(c)

	case S_block:
		l.block(c)

	case S_block_bq:
		if c == 96 {
			l.state = S_block
			l.p++
		} else {
			l.emit(T_ATTRIBUTE_WORD, p, l.until("`", p))
		}

	case S_acc_title, S_acc_descr:
		if l.state == S_acc_title {
			l.emit(T_acc_title_value, p, l.until("\n", p))
		} else {
			l.emit(T_acc_descr_value, p, l.until("\n", p))
		}
		l.state = S_INITIAL

	case S_acc_descr_multiline:
		if c == 125 {
			l.state = S_INITIAL
			l.p++
		} else {
			l.emit(T_acc_descr_multiline_value, p, l.until("}", p))
		}

	case S_style:
		src := l.src
		if c == 10 {
			e := p + 1
			for e < l.n && src[e] == 10 {
				e++
			}
			l.state = S_INITIAL
			l.emit(T_NEWLINE, p, e)
		} else if util.IsSpace(c) {
			l.p = l.skipWs(p)
		} else if c == 58 {
			l.emit(T_COLON, p, p+1)
		} else if c == 44 {
			l.emit(T_COMMA, p, p+1)
		} else if c == 35 {
			l.emit(T_BRKT, p, p+1)
		} else if c == 59 {
			l.emit(T_SEMI, p, p+1)
		} else if inClass(src, p, STYLE_TEXT) {
			e := p + 1
			for inClass(src, e, STYLE_TEXT) {
				e++
			}
			l.emit(T_STYLE_TEXT, p, e)
		} else {
			return false
		}
	}
	return true
}

func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src), dirNext: -2, lineEnd: -1, tildeNext: -2, tildeLine: -1, tildeLast: -1, wsNext: -1}
	t := &l.toks
	for l.p < l.n {
		if !l.step(l.code(l.p)) {
			t.types = append(t.types, T_ERROR)
			t.texts = append(t.texts, "")
			t.starts = append(t.starts, l.p)
			return *t
		}
	}

	// At the end of input Jison's INITIAL state yields EOF, the title and description states yield
	// an empty value, and the block and style states yield nothing.
	last := -1
	switch l.state {
	case S_INITIAL:
		last = T_EOF
	case S_acc_title:
		last = T_acc_title_value
	case S_acc_descr:
		last = T_acc_descr_value
	case S_acc_descr_multiline:
		last = T_acc_descr_multiline_value
	}
	if last != -1 {
		t.types = append(t.types, last)
		t.texts = append(t.texts, "")
		t.starts = append(t.starts, l.n)
	}
	t.types = append(t.types, T_END)
	t.texts = append(t.texts, "")
	t.starts = append(t.starts, l.n)
	return *t
}
