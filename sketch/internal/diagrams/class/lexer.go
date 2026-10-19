// Ported from pele src/diagrams/class/lexer.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// As in the flowchart and state lexers, the port reads UTF-8 bytes: code() decodes
// the character at a byte offset, and a step past a character that may not be
// ASCII steps the whole character. pele's closures are methods of a lexer here,
// which keeps each one out of line. RE_DIRECTION is written out as a hand matcher.

package class

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
	S_string
	S_bqstring
	S_generic
	S_callbackName
	S_callbackArgs
	S_accTitle
	S_accDescr
	S_accDescrMultiline
	S_cls
	S_classBody
	S_namespace
	S_namespaceBody
)

// The token each state gives at the end of input, before the parser sees the end itself.
var AT_END = [...]int{
	T_EOF,
	T_STR,
	T_EOF,
	T_GENERICTYPE,
	T_CALLBACK_NAME,
	T_CALLBACK_ARGS,
	T_acc_title_value,
	T_acc_descr_value,
	T_acc_descr_multiline_value,
	T_EOF,
	T_EOF_IN_STRUCT,
	T_EOF,
	T_EOF_IN_STRUCT,
}

var directionNames = [4]string{"TB", "BT", "RL", "LR"}

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
	stack []int
	state int
	p     int

	dirNext   int
	lineEnd   int
	dirFailed int
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

//go:noinline
func (l *lexer) kw(word string) bool {
	return strings.HasPrefix(l.src[l.p:], word) && !util.IsWord(l.code(l.p+len(word)))
}

//go:noinline
func (l *lexer) skipWs(q int) int {
	for q < l.n && util.IsSpace(l.code(q)) {
		q += sizeAt(l.src, q)
	}
	return q
}

//go:noinline
func (l *lexer) until(ch string) int {
	e := indexFrom(l.src, ch, l.p)
	if e == -1 {
		return l.n
	}
	return e
}

// A run of whitespace is a NEWLINE up to its last line feed; the rest is skipped.
func (l *lexer) space(leaves bool) {
	q := l.p
	k := -1
	for q < l.n {
		c := l.code(q)
		if !util.IsSpace(c) {
			break
		}
		if c == 10 {
			k = q
		}
		q += sizeAt(l.src, q)
	}
	if k < 0 {
		l.p = q
		return
	}
	if leaves {
		l.pop()
	}
	l.emit(T_NEWLINE, l.p, k+1)
}

// The keywords that every state recognizes, even inside generics and backtick names.
func (l *lexer) keyword(c rune) bool {
	p := l.p
	switch c {
	case 99:
		if l.kw("cssClass") {
			return l.emit(T_CSSCLASS, p, p+8)
		}
		if l.kw("callback") {
			return l.emit(T_CALLBACK, p, p+8)
		}
		if l.kw("click") {
			return l.emit(T_CLICK, p, p+5)
		}
	case 108:
		if l.kw("link") {
			return l.emit(T_LINK, p, p+4)
		}
	case 110:
		if l.kw("note for") {
			return l.emit(T_NOTE_FOR, p, p+8)
		}
		if l.kw("note") {
			return l.emit(T_NOTE, p, p+4)
		}
	case 60:
		if l.code(p+1) == 60 {
			return l.emit(T_ANNOTATION_START, p, p+2)
		}
	case 62:
		if l.code(p+1) == 62 {
			return l.emit(T_ANNOTATION_END, p, p+2)
		}
	case 104:
		if l.kw("href") {
			return l.emit(T_HREF, p, p+4)
		}
	}
	return false
}

func (l *lexer) common(c rune) bool {
	if l.keyword(c) {
		return true
	}
	p := l.p
	d := l.code(p + 1)
	switch c {
	case 34:
		l.push(S_string)
		l.p++
		return true
	case 126:
		l.push(S_generic)
		l.p++
		return true
	case 96:
		l.push(S_bqstring)
		l.p++
		return true
	case 95:
		if l.kw("_self") {
			return l.emit(T_LINK_TARGET, p, p+5)
		}
		if l.kw("_blank") {
			return l.emit(T_LINK_TARGET, p, p+6)
		}
		if l.kw("_parent") {
			return l.emit(T_LINK_TARGET, p, p+7)
		}
		if l.kw("_top") {
			return l.emit(T_LINK_TARGET, p, p+4)
		}
	case 60:
		if d == 124 {
			return l.emit(T_EXTENSION, p, p+2)
		}
		return l.emit(T_DEPENDENCY, p, p+1)
	case 124:
		return d == 62 && l.emit(T_EXTENSION, p, p+2)
	case 62:
		return l.emit(T_DEPENDENCY, p, p+1)
	case 42:
		return l.emit(T_COMPOSITION, p, p+1)
	case 111:
		if !util.IsWord(d) {
			return l.emit(T_AGGREGATION, p, p+1)
		}
	case 40:
		return d == 41 && l.emit(T_LOLLIPOP, p, p+2)
	case 45:
		if d == 45 {
			return l.emit(T_LINE, p, p+2)
		}
		return l.emit(T_MINUS, p, p+1)
	case 46:
		if d == 46 {
			return l.emit(T_DOTTED_LINE, p, p+2)
		}
		return l.emit(T_DOT, p, p+1)
	case 58:
		q := p + 1
		for q < l.n {
			ch := l.src[q]
			if ch == 58 || ch == 10 || ch == 59 {
				break
			}
			q++
		}
		if q > p+1 {
			return l.emit(T_LABEL, p, q)
		}
		return d == 58 && l.code(p+2) == 58 && l.emit(T_STYLE_SEPARATOR, p, p+3)
	case 43:
		return l.emit(T_PLUS, p, p+1)
	case 37:
		return l.emit(T_PCT, p, p+1)
	case 61:
		return l.emit(T_EQUALS, p, p+1)
	case 91:
		return l.emit(T_SQS, p, p+1)
	case 93:
		return l.emit(T_SQE, p, p+1)
	case 33, 35, 36, 38, 39, 44, 63, 92, 47:
		return l.emit(T_PUNCTUATION, p, p+1)
	}
	if util.IsWord(c) {
		q := p + 1
		for util.IsWord(l.code(q)) {
			q++
		}
		return l.emit(T_ALPHA, p, q)
	}
	return util.IsUnicodeLetter(c) && l.emit(T_UNICODE_TEXT, p, p+sizeAt(l.src, p))
}

// RE_DIRECTION[kind] at p: /.*direction\s+TB[^\n]*/y and its siblings. The greedy `.*` takes
// the last `direction` on the line that is followed by the direction, and the match runs to
// the line feed after it. Returns the end of the match, or -1.
func (l *lexer) matchDirection(kind int, le int) int {
	src := l.src
	name := directionNames[kind]
	end := -1
	for d := indexFrom(src, "direction", l.p); d != -1 && d < le; d = indexFrom(src, "direction", d+1) {
		w := l.skipWs(d + 9)
		if w == d+9 || !strings.HasPrefix(src[w:], name) {
			continue
		}
		end = indexFrom(src, "\n", w+2)
		if end == -1 {
			end = l.n
		}
	}
	return end
}

func (l *lexer) direction() bool {
	if l.dirNext == -1 || l.p < l.dirFailed {
		return false
	}
	if l.dirNext < l.p {
		l.dirNext = indexFrom(l.src, "direction", l.p)
	}
	if l.dirNext == -1 {
		return false
	}
	if l.lineEnd < l.p {
		l.lineEnd = reLineEnd(l.src, l.p)
		if l.lineEnd == -1 {
			l.lineEnd = l.n
		}
	}
	if l.dirNext >= l.lineEnd {
		return false
	}
	for i := 0; i < 4; i++ {
		if e := l.matchDirection(i, l.lineEnd); e != -1 {
			return l.emit(T_direction_tb+i, l.p, e)
		}
	}
	// No direction statement starts anywhere in the rest of this line either.
	l.dirFailed = l.lineEnd
	return false
}

func (l *lexer) initial(c rune) bool {
	if l.direction() {
		return true
	}
	if util.IsSpace(c) {
		l.space(false)
		return true
	}
	src := l.src
	p := l.p
	switch c {
	case 37:
		if l.code(p+1) == 37 {
			e := l.until("\n")
			for e < l.n && (src[e] == 10 || src[e] == 13) {
				e++
			}
			l.p = e
			return true
		}
	case 97:
		title := strings.HasPrefix(src[p:], "accTitle")
		if !title && !strings.HasPrefix(src[p:], "accDescr") {
			break
		}
		q := l.skipWs(p + 8)
		d := l.code(q)
		if d == 58 {
			if title {
				l.emit(T_acc_title, p, l.skipWs(q+1))
				l.push(S_accTitle)
			} else {
				l.emit(T_acc_descr, p, l.skipWs(q+1))
				l.push(S_accDescr)
			}
			return true
		}
		if d == 123 && !title {
			l.p = l.skipWs(q + 1)
			l.push(S_accDescrMultiline)
			return true
		}
	case 99:
		if l.kw("classDiagram-v2") {
			return l.emit(T_CLASS_DIAGRAM, p, p+15)
		}
		if l.kw("classDiagram") {
			return l.emit(T_CLASS_DIAGRAM, p, p+12)
		}
		if strings.HasPrefix(src[p:], "call") && util.IsSpace(l.code(p+4)) {
			l.p = l.skipWs(p + 4)
			l.push(S_callbackName)
			return true
		}
		if l.kw("classDef") {
			return l.emit(T_CLASSDEF, p, p+8)
		}
		if l.kw("class") {
			l.push(S_cls)
			return l.emit(T_CLASS, p, p+5)
		}
	case 91:
		if strings.HasPrefix(src[p+1:], "*]") {
			return l.emit(T_EDGE_STATE, p, p+3)
		}
	case 115:
		if l.kw("style") {
			return l.emit(T_STYLE, p, p+5)
		}
	case 110:
		if l.kw("namespace") {
			l.push(S_namespace)
			return l.emit(T_NAMESPACE, p, p+9)
		}
	case 44:
		return l.emit(T_COMMA, p, p+1)
	case 35:
		return l.emit(T_BRKT, p, p+1)
	}
	return l.common(c) || (c == 58 && l.emit(T_COLON, p, p+1))
}

// step lexes one token, or one skipped stretch, in the current state. It returns false on text
// that no rule of the state matches.
func (l *lexer) step(c rune) bool {
	src := l.src
	p := l.p
	switch l.state {
	case S_INITIAL:
		return l.initial(c)

	case S_cls:
		if util.IsSpace(c) {
			l.space(true)
		} else if c == 125 {
			l.pop()
			l.pop()
			l.emit(T_STRUCT_STOP, p, p+1)
		} else if c == 123 {
			l.push(S_classBody)
			l.emit(T_STRUCT_START, p, p+1)
		} else {
			return l.common(c)
		}

	case S_classBody:
		if c == 34 {
			l.push(S_string)
			l.p++
		} else if c == 125 {
			l.pop()
			l.emit(T_STRUCT_STOP, p, p+1)
		} else if c == 91 && strings.HasPrefix(src[p+1:], "*]") {
			l.emit(T_EDGE_STATE, p, p+3)
		} else if c == 123 {
			l.emit(T_OPEN_IN_STRUCT, p, p+1)
		} else if c == 10 {
			l.p++
		} else {
			q := p + 1
			for q < l.n {
				ch := src[q]
				if ch == 123 || ch == 125 || ch == 10 {
					break
				}
				q++
			}
			l.emit(T_MEMBER, p, q)
		}

	case S_namespace:
		if util.IsSpace(c) {
			l.space(true)
		} else if c == 123 {
			l.push(S_namespaceBody)
			l.emit(T_STRUCT_START, p, p+1)
		} else if c == 125 {
			// The brace is read again in the state the namespace was opened from.
			l.pop()
		} else if c == 110 && l.kw("namespace") {
			l.push(S_namespace)
			l.emit(T_NAMESPACE, p, p+9)
		} else {
			return l.common(c)
		}

	case S_namespaceBody:
		if util.IsSpace(c) {
			l.space(false)
		} else if c == 125 {
			l.pop()
			l.emit(T_STRUCT_STOP, p, p+1)
		} else if c == 110 && l.kw("namespace") {
			l.push(S_namespace)
			l.emit(T_NAMESPACE, p, p+9)
		} else if c == 91 && strings.HasPrefix(src[p+1:], "*]") {
			l.emit(T_EDGE_STATE, p, p+3)
		} else if c == 99 && l.kw("class") {
			l.push(S_cls)
			l.emit(T_CLASS, p, p+5)
		} else {
			return l.common(c)
		}

	case S_string:
		if c == 34 {
			l.pop()
			l.p++
		} else {
			l.emit(T_STR, p, l.until(`"`))
		}

	case S_generic:
		if c == 34 {
			l.push(S_string)
			l.p++
		} else if l.keyword(c) {
			break
		} else if c == 126 {
			l.pop()
			l.p++
		} else {
			l.emit(T_GENERICTYPE, p, l.until("~"))
		}

	case S_bqstring:
		if c == 34 {
			l.push(S_string)
			l.p++
		} else if l.keyword(c) {
			break
		} else if c == 126 {
			l.push(S_generic)
			l.p++
		} else if c == 96 {
			l.pop()
			l.p++
		} else {
			l.emit(T_BQUOTE_STR, p, l.until("`"))
		}

	case S_callbackName:
		if c == 40 {
			q := l.skipWs(p + 1)
			l.pop()
			if l.code(q) == 41 {
				l.p = q + 1
			} else {
				l.push(S_callbackArgs)
				l.p++
			}
		} else {
			l.emit(T_CALLBACK_NAME, p, l.until("("))
		}

	case S_callbackArgs:
		if c == 41 {
			l.pop()
			l.p++
		} else {
			l.emit(T_CALLBACK_ARGS, p, l.until(")"))
		}

	case S_accTitle, S_accDescr:
		if l.state == S_accTitle {
			l.emit(T_acc_title_value, p, l.until("\n"))
		} else {
			l.emit(T_acc_descr_value, p, l.until("\n"))
		}
		l.pop()

	case S_accDescrMultiline:
		if c == 125 {
			l.pop()
			l.p++
		} else {
			l.emit(T_acc_descr_multiline_value, p, l.until("}"))
		}
	}
	return true
}

func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src), dirNext: strings.Index(src, "direction"), lineEnd: -1}
	t := &l.toks
	for {
		if l.p >= l.n {
			t.types = append(t.types, AT_END[l.state])
			t.texts = append(t.texts, "")
			t.starts = append(t.starts, l.n)
			break
		}
		if !l.step(l.code(l.p)) {
			t.types = append(t.types, T_ERROR)
			t.texts = append(t.texts, "")
			t.starts = append(t.starts, l.p)
			return *t
		}
	}
	t.types = append(t.types, T_END)
	t.texts = append(t.texts, "")
	t.starts = append(t.starts, l.n)
	return *t
}
