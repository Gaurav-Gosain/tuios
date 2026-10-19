// Ported from pele src/diagrams/gantt/lexer.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// As in the other lexers, the port reads UTF-8 bytes: code() decodes the character at a
// byte offset, and a step past a character that may not be ASCII steps the whole
// character. pele's closures are methods of a lexer here. The sticky regular expressions
// are written out as hand matchers.

package gantt

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
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
	S_href
	S_callbackname
	S_callbackargs
	S_click
)

var DAYS = [7]string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// A statement that starts with a keyword: the keyword, lower case, and what may follow it.
const (
	kwBoundary = iota // the keyword then \b
	kwValue           // \s[^#\n;]+
	kwMarker          // \s[^\n;]+
	kwLine            // \s[^\n]+
)

type keywordRule struct {
	word string
	kind int
	typ  int
}

// Statements that start with a keyword, grouped by the keyword's first letter and kept in the grammar's rule order.
func KEYWORDS(c rune) []keywordRule {
	switch c {
	case 97:
		return []keywordRule{{"axisformat", kwValue, T_axisFormat}, {"accdescription", kwValue, T_accDescription}}
	case 103:
		return []keywordRule{{"gantt", kwBoundary, T_gantt}}
	case 100:
		return []keywordRule{{"dateformat", kwValue, T_dateFormat}}
	case 105:
		return []keywordRule{{"inclusiveenddates", kwBoundary, T_inclusiveEndDates}, {"includes", kwValue, T_includes}}
	case 116:
		return []keywordRule{
			{"topaxis", kwBoundary, T_topAxis},
			{"tickinterval", kwValue, T_tickInterval},
			{"todaymarker", kwMarker, T_todayMarker},
			{"title", kwLine, T_title},
		}
	case 101:
		return []keywordRule{{"excludes", kwValue, T_excludes}}
	case 115:
		return []keywordRule{{"section", kwLine, T_section}}
	}
	return nil
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

type lexer struct {
	src   string
	n     int
	toks  Tokens
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

//go:noinline
func (l *lexer) until(ch byte, from int) int {
	if from >= l.n {
		return l.n
	}
	e := strings.IndexByte(l.src[from:], ch)
	if e == -1 {
		return l.n
	}
	return from + e
}

//go:noinline
func (l *lexer) skipWs(q int) int {
	for q < l.n && util.IsSpace(l.code(q)) {
		q += sizeAt(l.src, q)
	}
	return q
}

// space matches one \s at q and returns its end, or -1.
func (l *lexer) space(q int) int {
	if q < l.n && util.IsSpace(l.code(q)) {
		return q + sizeAt(l.src, q)
	}
	return -1
}

// boundary is \b after a word character at q-1.
func (l *lexer) boundary(q int) bool { return !util.IsWord(l.code(q)) }

// accAt matches /accTitle\s*:\s*/iy and its siblings at p: a word, spaces, the mark and
// spaces. Returns the end of the match, or -1.
func (l *lexer) accAt(word string, mark byte) int {
	if !hasFold(l.src, l.p, word) {
		return -1
	}
	q := l.skipWs(l.p + len(word))
	if q >= l.n || l.src[q] != mark {
		return -1
	}
	return l.skipWs(q + 1)
}

// spaced matches /word\s+/iy at p and returns its end, or -1.
func (l *lexer) spaced(word string) int {
	if !hasFold(l.src, l.p, word) || l.space(l.p+len(word)) < 0 {
		return -1
	}
	return l.skipWs(l.p + len(word))
}

// RE_HREF: /href\s+"/iy.
func (l *lexer) atHref() int {
	q := l.spaced("href")
	if q < 0 || q >= l.n || l.src[q] != '"' {
		return -1
	}
	return q + 1
}

// RE_EMPTY_CALL: /\(\s*\)/y.
func (l *lexer) atEmptyCall() int {
	if l.code(l.p) != 40 {
		return -1
	}
	q := l.skipWs(l.p + 1)
	if l.code(q) != 41 {
		return -1
	}
	return q + 1
}

// RE_DATE: /\d\d\d\d-\d\d-\d\d\b/y.
func (l *lexer) atDate() int {
	p := l.p
	for i, c := range "dddd-dd-dd" {
		ch := l.code(p + i)
		if c == '-' {
			if ch != '-' {
				return -1
			}
		} else if !util.IsDigit(ch) {
			return -1
		}
	}
	if !l.boundary(p + 10) {
		return -1
	}
	return p + 10
}

// One of KEYWORDS' patterns at p. Returns the end of the match, or -1.
func (l *lexer) atRule(rule keywordRule) int {
	if !hasFold(l.src, l.p, rule.word) {
		return -1
	}
	q := l.p + len(rule.word)
	if rule.kind == kwBoundary {
		if !l.boundary(q) {
			return -1
		}
		return q
	}
	q = l.space(q)
	if q < 0 {
		return -1
	}
	e := q
	for e < l.n {
		c := l.src[e]
		if c == '\n' || (rule.kind != kwLine && c == ';') || (rule.kind == kwValue && c == '#') {
			break
		}
		e++
	}
	if e == q {
		return -1
	}
	return e
}

func (l *lexer) keyword(c rune) bool {
	switch c | 32 {
	case 97:
		if e := l.accAt("acctitle", ':'); e >= 0 {
			l.state = S_acc_title
			l.emit(T_acc_title, l.p, e)
			return true
		}
		if e := l.accAt("accdescr", ':'); e >= 0 {
			l.state = S_acc_descr
			l.emit(T_acc_descr, l.p, e)
			return true
		}
		if e := l.accAt("accdescr", '{'); e >= 0 {
			l.state = S_acc_descr_multiline
			l.p = e
			return true
		}
	case 104:
		if e := l.atHref(); e >= 0 {
			l.state = S_href
			l.p = e
			return true
		}
	case 99:
		if e := l.spaced("call"); e >= 0 {
			l.state = S_callbackname
			l.p = e
			return true
		}
		if e := l.spaced("click"); e >= 0 {
			l.state = S_click
			l.p = e
			return true
		}
	case 119:
		if q := l.spaced("weekday"); q >= 0 {
			for i, day := range DAYS {
				if hasFold(l.src, q, day) && l.boundary(q+len(day)) {
					l.emit(T_weekday_monday+i, l.p, q+len(day))
					return true
				}
			}
		}
		if q := l.spaced("weekend"); q >= 0 {
			if hasFold(l.src, q, "friday") && l.boundary(q+6) {
				l.emit(T_weekend_friday, l.p, q+6)
				return true
			}
			if hasFold(l.src, q, "saturday") && l.boundary(q+8) {
				l.emit(T_weekend_saturday, l.p, q+8)
				return true
			}
		}
	}
	if c >= 128 {
		return false
	}
	for _, rule := range KEYWORDS(c | 32) {
		if e := l.atRule(rule); e >= 0 {
			l.emit(rule.typ, l.p, e)
			return true
		}
	}
	return false
}

func tokenize(src string) Tokens {
	l := &lexer{src: src, n: len(src), state: S_INITIAL}
	n := l.n

	for {
		if l.p >= n {
			typ := T_EOF
			switch l.state {
			case S_acc_title:
				typ = T_acc_title_value
			case S_acc_descr:
				typ = T_acc_descr_value
			case S_acc_descr_multiline:
				typ = T_acc_descr_multiline_value
			case S_href:
				typ = T_href
			case S_callbackname:
				typ = T_callbackname
			case S_callbackargs:
				typ = T_callbackargs
			case S_click:
				typ = T_click
			}
			l.emit(typ, n, n)
			break
		}

		p := l.p
		c := l.code(p)

		switch l.state {
		case S_INITIAL:
			// The UTF-16 unit after c: the low surrogate when c is outside the BMP.
			d := rune(0xdc00)
			if c != 0xd800 {
				d = l.code(p + sizeAt(src, p))
			}
			if c == 37 {
				if d != 37 {
					e := l.until('\n', p+1)
					for l.code(e) == 10 {
						e++
					}
					l.p = e
				} else if l.code(p+2) == 123 {
					// The grammar enters a lexer state it never defines, so nothing can follow.
					l.emit(T_open_directive, p, p+3)
					l.emit(T_ERROR, p, p)
					return l.toks
				} else {
					l.p = l.until('\n', p+2)
				}
			} else if c != 125 && d == 37 {
				// Any character but `}` followed by `%` starts a comment that runs to the end of the line.
				l.p = l.until('\n', p+sizeAt(src, p)+1)
			} else if c == 10 {
				e := p + 1
				for l.code(e) == 10 {
					e++
				}
				l.emit(T_NL, p, e)
			} else if util.IsSpace(c) {
				l.p = l.skipWs(p)
			} else if l.keyword(c) {
				break
			} else if c >= 48 && c <= 57 && l.atDate() >= 0 {
				l.emit(T_date, p, p+10)
			} else if c != 58 {
				e := p + 1
				for e < n {
					ch := src[e]
					if ch == 58 || ch == 10 {
						break
					}
					e++
				}
				l.emit(T_taskTxt, p, e)
			} else {
				e := p + 1
				for e < n {
					ch := src[e]
					if ch == 35 || ch == 10 || ch == 59 {
						break
					}
					e++
				}
				typ := T_COLON
				if e > p+1 {
					typ = T_taskData
				}
				l.emit(typ, p, e)
			}

		case S_acc_title, S_acc_descr:
			typ := T_acc_descr_value
			if l.state == S_acc_title {
				typ = T_acc_title_value
			}
			l.state = S_INITIAL
			l.emit(typ, p, l.until('\n', p))

		case S_acc_descr_multiline:
			if c == 125 {
				l.state = S_INITIAL
				l.p++
			} else {
				l.emit(T_acc_descr_multiline_value, p, l.until('}', p))
			}

		case S_href:
			if c == 34 {
				l.state = S_INITIAL
				l.p++
			} else {
				l.emit(T_href, p, l.until('"', p))
			}

		case S_callbackname:
			if c == 40 {
				if e := l.atEmptyCall(); e >= 0 {
					l.state = S_INITIAL
					l.p = e
				} else {
					l.state = S_callbackargs
					l.p++
				}
			} else {
				l.emit(T_callbackname, p, l.until('(', p))
			}

		case S_callbackargs:
			if c == 41 {
				l.state = S_INITIAL
				l.p++
			} else {
				l.emit(T_callbackargs, p, l.until(')', p))
			}

		case S_click:
			if util.IsSpace(c) {
				l.state = S_INITIAL
				l.p += sizeAt(src, p)
			} else {
				e := p + sizeAt(src, p)
				for e < n && !util.IsSpace(l.code(e)) {
					e += sizeAt(src, e)
				}
				l.emit(T_click, p, e)
			}
		}
	}

	l.toks.types = append(l.toks.types, T_END)
	l.toks.texts = append(l.toks.texts, "")
	l.toks.starts = append(l.toks.starts, n)
	return l.toks
}
