// Ported from pele src/diagrams/agentflow/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele reads UTF-16 code units with charCodeAt. The port reads the UTF-8 bytes:
// code() decodes the character at a byte offset, and where pele steps one unit
// past a character that may not be ASCII, the port steps the whole character.
// Token starts are byte offsets; the columns of locs count UTF-16 units, as pele's do.

package agentflow

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
	"tuios.dev/sketch/internal/util"
)

type Tokens struct {
	types  []int
	texts  []string
	starts []int
	// Four numbers per token, counted the way Jison counts them:
	// first line, first column, last line, last column. Lines start at 1, columns at 0.
	locs []int
}

// Lexer states.
const (
	S_INITIAL = iota
	S_string
	S_md_string
	S_acc_title
	S_acc_descr
	S_acc_descr_multiline
	S_dir
	S_text
	S_ellipseText
	S_trapText
	S_edgeText
	S_click
	S_callbackname
	S_callbackargs
	S_shapeData
	S_shapeDataStr
)

var NODE_CHAR = func() [128]uint8 {
	var t [128]uint8
	for _, ch := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!\"#$%&'*+.`?\\_/" {
		t[ch] = 1
	}
	return t
}()

const sp = `[` + js.S + `]`

// sticky compiles a pattern that pele runs with the y flag: it must match at the position.
func sticky(pattern string) *lazyre.Regexp { return lazyre.New(`^(?:` + pattern + `)`) }

var (
	RE_ACC_TITLE    = sticky(`accTitle` + sp + `*:` + sp + `*`)
	RE_ACC_DESCR    = sticky(`accDescr` + sp + `*:` + sp + `*`)
	RE_ACC_DESCR_ML = sticky(`accDescr` + sp + `*\{` + sp + `*`)
	RE_CALL         = sticky(`call` + sp + `+`)
	RE_CLICK        = sticky(`click` + sp + `+`)
	RE_NODIR        = sticky(`(?:\r?\n)*` + sp + `*\n`)
	RE_DIR          = sticky(sp + `*(?:LR|RL|TB|BT|TD|BR)\b`)
	RE_DIR_SYM      = sticky(sp + `*[<>^]`)
	RE_DIR_V        = sticky(sp + `*v\b`)
	RE_DIRECTION    = [5]*lazyre.Regexp{
		sticky(js.Dot + `*direction` + sp + `+TB[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+BT[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+RL[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+LR[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+TD[^\n]*`),
	}
	RE_EMPTY_CALL = sticky(`\(` + sp + `*\)`)
	RE_BR         = lazyre.New(`\n` + sp + `*`)
)

// lexer is the state of one run of tokenize: the tokens so far, the mode stack and the read position.
type lexer struct {
	src    string
	n      int
	types  []int
	texts  []string
	starts []int
	locs   []int
	stack  []int
	state  int
	p      int
	line   int
	col    int

	firstGraph bool
	dirNext    int
	atNext     int
	lineEnd    int
	dirFailed  int
	runEnd     int
	runAt      int
	wsFrom     int
	wsTo       int
	blankFrom  int
	blankTo    int
	noLink     int
	noArrow    int
}

// code is the character at byte q, as charCodeAt would give it, or -1 past the end.
// A byte inside a character gives 0xfffe, which no test matches.
func (l *lexer) code(q int) rune {
	if q < 0 || q >= l.n {
		return -1
	}
	b := l.src[q]
	if b < utf8.RuneSelf {
		return rune(b)
	}
	r, _ := utf8.DecodeRuneInString(l.src[q:])
	if r == utf8.RuneError {
		return 0xfffe
	}
	if r > 0xffff {
		// A high surrogate, as charCodeAt gives for the first unit.
		return 0xd800
	}
	return r
}

// size is how many bytes the character at q takes: one step of pele's.
func (l *lexer) size(q int) int {
	if q >= l.n || l.src[q] < utf8.RuneSelf {
		return 1
	}
	_, s := utf8.DecodeRuneInString(l.src[q:])
	return s
}

// units is how many UTF-16 units src[a:b] holds.
func (l *lexer) units(a, b int) int {
	u := 0
	for k := a; k < b; {
		if l.src[k] < utf8.RuneSelf {
			u++
			k++
			continue
		}
		r, s := utf8.DecodeRuneInString(l.src[k:b])
		if r > 0xffff {
			u += 2
		} else {
			u++
		}
		k += s
	}
	return u
}

//go:noinline
func (l *lexer) isSpace(c rune) bool { return util.IsSpace(c) }

// advance moves over one match. Jison counts \r\n, \r and \n as one line break each, and after a
// break its column stops at the first U+2028 or U+2029.
func (l *lexer) advance(e int) {
	brk := -1
	for k := l.p; k < e; k++ {
		ch := l.src[k]
		if ch == 10 {
			l.line++
			brk = k
		} else if ch == 13 {
			if k+1 < e && l.src[k+1] == 10 {
				k++
			}
			l.line++
			brk = k
		}
	}
	if brk < 0 {
		l.col += l.units(l.p, e)
	} else {
		k := brk + 1
		for k < e {
			r := l.code(k)
			if r == 0x2028 || r == 0x2029 {
				break
			}
			k += l.size(k)
		}
		l.col = l.units(brk+1, k)
	}
	l.p = e
}

func (l *lexer) emit(typ int, e int) {
	l.types = append(l.types, typ)
	l.texts = append(l.texts, l.src[l.p:e])
	l.starts = append(l.starts, l.p)
	l.locs = append(l.locs, l.line, l.col)
	l.advance(e)
	l.locs = append(l.locs, l.line, l.col)
}

// emitFlat is for a token that cannot hold a line break.
func (l *lexer) emitFlat(typ int, e int) {
	l.types = append(l.types, typ)
	l.texts = append(l.texts, l.src[l.p:e])
	l.starts = append(l.starts, l.p)
	l.locs = append(l.locs, l.line, l.col)
	l.col += l.units(l.p, e)
	l.p = e
	l.locs = append(l.locs, l.line, l.col)
}

// emitChars is for a run that Jison matches one character at a time, where \r\n counts as two line breaks.
func (l *lexer) emitChars(typ int, e int) {
	l.types = append(l.types, typ)
	l.texts = append(l.texts, l.src[l.p:e])
	l.starts = append(l.starts, l.p)
	l.locs = append(l.locs, l.line, l.col)
	for k := l.p; k < e; {
		ch := l.src[k]
		if ch == 10 || ch == 13 {
			l.line++
			l.col = 0
			k++
			continue
		}
		l.col += l.units(k, k+l.size(k))
		k += l.size(k)
	}
	l.p = e
	l.locs = append(l.locs, l.line, l.col)
}

func (l *lexer) emitText(typ int, text string, e int) {
	l.types = append(l.types, typ)
	l.texts = append(l.texts, text)
	l.starts = append(l.starts, l.p)
	l.locs = append(l.locs, l.line, l.col)
	l.advance(e)
	l.locs = append(l.locs, l.line, l.col)
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

func (l *lexer) at(re *lazyre.Regexp, q int) int {
	if q > l.n {
		return -1
	}
	loc := re.FindStringIndex(l.src[q:])
	if loc == nil {
		return -1
	}
	return q + loc[1]
}

func (l *lexer) kw(word string) bool {
	return strings.HasPrefix(l.src[l.p:], word) && !util.IsWord(l.code(l.p+len(word)))
}

// wsEnd is the end of the run of whitespace starting at q. The last run is kept, so asking again from inside it costs nothing.
func (l *lexer) wsEnd(q int) int {
	if q >= l.wsFrom && q <= l.wsTo {
		return l.wsTo
	}
	j := q
	for j < l.n && l.isSpace(l.code(j)) {
		j += l.size(j)
	}
	l.wsFrom = q
	l.wsTo = j
	return j
}

func (l *lexer) blankEnd(q int) int {
	if q >= l.blankFrom && q <= l.blankTo {
		return l.blankTo
	}
	j := q
	for j < l.n {
		ch := l.src[j]
		if ch != 32 && ch != 9 {
			break
		}
		j++
	}
	l.blankFrom = q
	l.blankTo = j
	return j
}

// arrowEnd is the end of `\s*--+[x>]\s*` starting at q, or -1.
func (l *lexer) arrowEnd(q int) int {
	j := l.wsEnd(q)
	if j == l.noArrow || l.code(j) != 45 || l.code(j+1) != 45 {
		return -1
	}
	k := j + 2
	for l.code(k) == 45 {
		k++
	}
	t := l.code(k)
	if t == 120 || t == 62 {
		return l.wsEnd(k + 1)
	}
	l.noArrow = j
	return -1
}

func (l *lexer) link() bool {
	j := l.wsEnd(l.p)
	if j == l.noLink || l.code(j) != 45 {
		return false
	}
	d := l.code(j + 1)
	k := j + 2
	if d == 45 {
		for l.code(k) == 45 {
			k++
		}
		t := l.code(k)
		if t == 120 || t == 62 {
			l.emit(T_LINK, l.wsEnd(k+1))
		} else {
			l.push(S_edgeText)
			l.emit(T_START_LINK, l.wsEnd(j+2))
		}
		return true
	}
	if d == 46 {
		for l.code(k) == 46 {
			k++
		}
		if l.code(k) == 45 {
			l.emit(T_LINK, l.wsEnd(k+1))
			return true
		}
	}
	l.noLink = j
	return false
}

// commentEnd: `%%` not followed by `{`, through the end of the line.
func (l *lexer) commentEnd(q int) int {
	if l.code(q) != 37 || l.code(q+1) != 37 || l.code(q+2) == 123 {
		return -1
	}
	if e := strings.IndexByte(l.src[q+2:], '\n'); e >= 0 {
		return q + 2 + e
	}
	return l.n
}

func (l *lexer) quote() {
	if l.code(l.p+1) == 96 {
		l.push(S_md_string)
		l.advance(l.p + 2)
	} else {
		l.push(S_string)
		l.advance(l.p + 1)
	}
}

func (l *lexer) opener(c rune) bool {
	d := l.code(l.p + 1)
	if c == 40 {
		switch {
		case d == 45:
			l.push(S_ellipseText)
			l.emitFlat(T_ELLIPSE_START, l.p+2)
		case d == 91:
			l.push(S_text)
			l.emitFlat(T_STADIUMSTART, l.p+2)
		case d == 40 && l.code(l.p+2) == 40:
			l.push(S_text)
			l.emitFlat(T_DOUBLECIRCLESTART, l.p+3)
		default:
			l.push(S_text)
			l.emitFlat(T_PS, l.p+1)
		}
		return true
	}
	if c == 91 {
		switch d {
		case 91:
			l.push(S_text)
			l.emitFlat(T_SUBROUTINESTART, l.p+2)
		case 40:
			l.push(S_text)
			l.emitFlat(T_CYLINDERSTART, l.p+2)
		case 47:
			l.push(S_trapText)
			l.emitFlat(T_TRAPSTART, l.p+2)
		case 92:
			l.push(S_trapText)
			l.emitFlat(T_INVTRAPSTART, l.p+2)
		default:
			l.push(S_text)
			l.emitFlat(T_SQS, l.p+1)
		}
		return true
	}
	if c == 124 {
		l.push(S_text)
		l.emitFlat(T_PIPE, l.p+1)
		return true
	}
	if c == 123 {
		l.push(S_text)
		l.emitFlat(T_DIAMOND_START, l.p+1)
		return true
	}
	return false
}

func (l *lexer) nodeString() bool {
	q := l.p
	for q < l.n {
		ch := l.src[q]
		if ch < 128 && NODE_CHAR[ch] == 1 {
			q++
		} else if ch == 45 && q+1 < l.n {
			d := l.src[q+1]
			if d == 62 || d == 45 || d == 46 {
				break
			}
			q++
		} else {
			break
		}
	}
	if q == l.p {
		return false
	}
	l.emitFlat(T_NODE_STRING, q)
	return true
}

//go:noinline
func (l *lexer) blanks(from int) int {
	e := from
	for e < l.n {
		ch := l.src[e]
		if ch != 32 && ch != 9 {
			break
		}
		e++
	}
	return e
}

func (l *lexer) initial(c rune) bool {
	var e int
	switch c {
	case 97:
		if strings.HasPrefix(l.src[l.p:], "acc") {
			if e = l.at(RE_ACC_TITLE, l.p); e >= 0 {
				l.push(S_acc_title)
				l.emit(T_acc_title, e)
				return true
			}
			if e = l.at(RE_ACC_DESCR, l.p); e >= 0 {
				l.push(S_acc_descr)
				l.emit(T_acc_descr, e)
				return true
			}
			if e = l.at(RE_ACC_DESCR_ML, l.p); e >= 0 {
				l.push(S_acc_descr_multiline)
				l.advance(e)
				return true
			}
		}
		if l.kw("agentflow-beta") {
			if l.firstGraph {
				l.firstGraph = false
				l.push(S_dir)
			}
			l.emitFlat(T_GRAPH, l.p+14)
			return true
		}
	case 64:
		if l.code(l.p+1) == 123 {
			l.push(S_shapeData)
			l.emitText(T_SHAPE_DATA, "", l.p+2)
			return true
		}
	case 99:
		if e = l.at(RE_CALL, l.p); e >= 0 {
			l.push(S_callbackname)
			l.advance(e)
			return true
		}
		if l.kw("classDef") {
			l.emitFlat(T_CLASSDEF, l.p+8)
			return true
		}
		if l.kw("class") {
			l.emitFlat(T_CLASS, l.p+5)
			return true
		}
		if e = l.at(RE_CLICK, l.p); e >= 0 {
			l.push(S_click)
			l.advance(e)
			return true
		}
		if l.kw("connector") {
			l.emitFlat(T_connector, l.p+9)
			return true
		}
	case 34:
		l.quote()
		return true
	case 115:
		if l.kw("style") {
			l.emitFlat(T_STYLE, l.p+5)
			return true
		}
	case 100:
		if l.kw("default") {
			l.emitFlat(T_DEFAULT, l.p+7)
			return true
		}
	case 108:
		if l.kw("linkStyle") {
			l.emitFlat(T_LINKSTYLE, l.p+9)
			return true
		}
	case 105:
		if l.kw("interpolate") {
			l.emitFlat(T_INTERPOLATE, l.p+11)
			return true
		}
	case 104:
		if strings.HasPrefix(l.src[l.p:], "href") && l.isSpace(l.code(l.p+4)) {
			l.emit(T_HREF, l.p+4+l.size(l.p+4))
			return true
		}
	case 102:
		if l.kw("flow") {
			l.emitFlat(T_flow, l.p+4)
			return true
		}
	case 103:
		if l.kw("global") {
			l.emitFlat(T_global, l.blanks(l.p+6))
			return true
		}
	case 101:
		if l.kw("end") {
			l.emitFlat(T_end, l.blanks(l.p+3))
			return true
		}
	case 95:
		for _, target := range [...]string{"_self", "_blank", "_parent", "_top"} {
			if l.kw(target) {
				l.emitFlat(T_LINK_TARGET, l.p+len(target))
				return true
			}
		}
	}

	if l.dirNext != -1 && l.p >= l.dirFailed {
		if l.dirNext < l.p {
			l.dirNext = strings.Index(l.src[l.p:], "direction")
			if l.dirNext >= 0 {
				l.dirNext += l.p
			}
		}
		if l.dirNext != -1 {
			if l.lineEnd < l.p {
				// /[\n\r  ]/g from p.
				l.lineEnd = l.n
				for k := l.p; k < l.n; k += l.size(k) {
					if r := l.code(k); r == 10 || r == 13 || r == 0x2028 || r == 0x2029 {
						l.lineEnd = k
						break
					}
				}
			}
			if l.dirNext < l.lineEnd {
				for i := 0; i < 5; i++ {
					if e = l.at(RE_DIRECTION[i], l.p); e >= 0 {
						l.emit(T_direction_tb+i, e)
						return true
					}
				}
				// No direction statement starts anywhere in the rest of this line either.
				l.dirFailed = l.lineEnd
			}
		}
	}

	// A link id is a run of non-space characters up to its last `@` that is not followed by `{` or `"`.
	if l.atNext != -1 && c != 34 && !l.isSpace(c) {
		if l.p >= l.runEnd {
			if l.atNext < l.p {
				l.atNext = strings.IndexByte(l.src[l.p:], '@')
				if l.atNext >= 0 {
					l.atNext += l.p
				}
			}
			q := l.p
			for q < l.n {
				ch := l.code(q)
				if ch == 34 || l.isSpace(ch) {
					break
				}
				q += l.size(q)
			}
			l.runEnd = q
			l.runAt = -1
			if l.atNext != -1 && l.atNext < q {
				for k := min(q, l.n-1) - 1; k >= l.atNext; k-- {
					if l.src[k] != 64 {
						continue
					}
					after := l.code(k + 1)
					if after != 123 && after != 34 {
						l.runAt = k
						break
					}
				}
			}
		}
		if l.runAt > l.p {
			l.emitFlat(T_LINK_ID, l.runAt+1)
			return true
		}
	}

	if c >= 48 && c <= 57 {
		q := l.p + 1
		for q < l.n && l.src[q] >= 48 && l.src[q] <= 57 {
			q++
		}
		l.emitFlat(T_NUM, q)
		return true
	}

	switch c {
	case 35:
		l.emitFlat(T_BRKT, l.p+1)
		return true
	case 58:
		if l.code(l.p+1) == 58 && l.code(l.p+2) == 58 {
			l.emitFlat(T_STYLE_SEPARATOR, l.p+3)
		} else {
			l.emitFlat(T_COLON, l.p+1)
		}
		return true
	case 38:
		l.emitFlat(T_AMP, l.p+1)
		return true
	case 59:
		l.emitFlat(T_SEMI, l.p+1)
		return true
	case 44:
		l.emitFlat(T_COMMA, l.p+1)
		return true
	case 42:
		l.emitFlat(T_MULT, l.p+1)
		return true
	case 60:
		l.emitFlat(T_TAGSTART, l.p+1)
		return true
	case 45:
		if l.link() || l.nodeString() {
			return true
		}
		l.emitFlat(T_MINUS, l.p+1)
		return true
	case 40:
		return l.opener(c)
	case 91:
		if l.code(l.p+1) == 124 {
			l.emitFlat(T_VERTEX_WITH_PROPS_START, l.p+2)
			return true
		}
		return l.opener(c)
	case 124, 123:
		return l.opener(c)
	case 62:
		l.push(S_text)
		l.emitFlat(T_TAGEND, l.p+1)
		return true
	case 94:
		l.emitFlat(T_UP, l.p+1)
		return true
	case 92:
		if l.code(l.p+1) == 124 {
			l.emitFlat(T_SEP, l.p+2)
			return true
		}
		return l.nodeString()
	case 118:
		if !util.IsWord(l.code(l.p + 1)) {
			l.emitFlat(T_DOWN, l.p+1)
			return true
		}
		return l.nodeString()
	case 37:
		if e = l.commentEnd(l.p); e >= 0 {
			l.emit(T_COMMENT, e)
			return true
		}
		return l.nodeString()
	}

	if l.isSpace(c) {
		if l.link() {
			return true
		}
		if c == 32 || c == 9 {
			if e = l.commentEnd(l.blankEnd(l.p)); e >= 0 {
				l.emit(T_COMMENT, e)
				return true
			}
		}
		e = l.p
		for {
			ch := l.code(e)
			if ch == 10 {
				e++
			} else if ch == 13 && l.code(e+1) == 10 {
				e += 2
			} else {
				break
			}
		}
		if e > l.p {
			l.emit(T_NEWLINE, e)
		} else {
			l.emit(T_SPACE, l.p+l.size(l.p))
		}
		return true
	}

	if c < 128 {
		return l.nodeString()
	}
	if util.IsUnicodeLetter(c) {
		l.emitFlat(T_UNICODE_TEXT, l.p+l.size(l.p))
		return true
	}
	return false
}

func tokenize(src string) Tokens {
	n := len(src)
	// About one token for every three bytes.
	guess := n/3 + 8
	l := &lexer{
		src:        src,
		n:          n,
		types:      make([]int, 0, guess),
		texts:      make([]string, 0, guess),
		starts:     make([]int, 0, guess),
		locs:       make([]int, 0, 4*guess),
		state:      S_INITIAL,
		line:       1,
		firstGraph: true,
		dirNext:    strings.Index(src, "direction"),
		atNext:     strings.Index(src, "@"),
		lineEnd:    -1,
		runAt:      -1,
		wsFrom:     -1,
		wsTo:       -1,
		blankFrom:  -1,
		blankTo:    -1,
		noLink:     -1,
		noArrow:    -1,
	}
	for {
		if l.p >= l.n {
			typ := -1
			switch l.state {
			case S_INITIAL:
				typ = T_EOF
			case S_acc_title:
				typ = T_acc_title_value
			case S_acc_descr:
				typ = T_acc_descr_value
			case S_acc_descr_multiline:
				typ = T_acc_descr_multiline_value
			case S_click:
				typ = T_CLICK
			case S_callbackname:
				typ = T_CALLBACKNAME
			case S_callbackargs:
				typ = T_CALLBACKARGS
			}
			if typ != -1 {
				l.emitFlat(typ, l.n)
			}
			break
		}

		c := l.code(l.p)
		ok := true

		switch l.state {
		case S_INITIAL:
			ok = l.initial(c)

		case S_string:
			if c == 34 {
				if l.code(l.p+1) == 96 {
					l.push(S_md_string)
					l.advance(l.p + 2)
				} else {
					l.pop()
					l.advance(l.p + 1)
				}
			} else {
				e := strings.IndexByte(l.src[l.p:], '"')
				if e == -1 {
					e = l.n
				} else {
					e += l.p
				}
				l.emit(T_STR, e)
			}

		case S_md_string:
			switch c {
			case 96:
				if l.code(l.p+1) == 34 {
					l.pop()
					l.advance(l.p + 2)
				} else {
					ok = false
				}
			case 34:
				l.quote()
			default:
				q := l.p + 1
				for q < l.n && l.src[q] != 96 && l.src[q] != 34 {
					q++
				}
				l.emit(T_MD_STR, q)
			}

		case S_acc_title, S_acc_descr:
			e := strings.IndexByte(l.src[l.p:], '\n')
			if e == -1 {
				e = l.n
			} else {
				e += l.p
			}
			typ := T_acc_descr_value
			if l.state == S_acc_title {
				typ = T_acc_title_value
			}
			l.pop()
			l.emit(typ, e)

		case S_acc_descr_multiline:
			if c == 125 {
				l.pop()
				l.advance(l.p + 1)
			} else {
				e := strings.IndexByte(l.src[l.p:], '}')
				if e == -1 {
					e = l.n
				} else {
					e += l.p
				}
				l.emit(T_acc_descr_multiline_value, e)
			}

		case S_dir:
			var e int
			if c == 34 {
				l.quote()
			} else if e = l.at(RE_NODIR, l.p); e >= 0 {
				l.pop()
				l.emit(T_NODIR, e)
			} else if e = l.at(RE_DIR, l.p); e >= 0 {
				l.pop()
				l.emit(T_DIR, e)
			} else if e = l.at(RE_DIR_SYM, l.p); e >= 0 {
				l.pop()
				l.emit(T_DIR, e)
			} else if e = l.at(RE_DIR_V, l.p); e >= 0 {
				l.pop()
				l.emit(T_DIR, e)
			} else {
				ok = l.opener(c)
			}

		case S_text:
			d := l.code(l.p + 1)
			switch c {
			case 34:
				l.quote()
			case 40, 91, 123:
				l.opener(c)
			case 93:
				l.pop()
				if d == 41 {
					l.emitFlat(T_STADIUMEND, l.p+2)
				} else if d == 93 {
					l.emitFlat(T_SUBROUTINEEND, l.p+2)
				} else {
					l.emitFlat(T_SQE, l.p+1)
				}
			case 41:
				l.pop()
				if d == 93 {
					l.emitFlat(T_CYLINDEREND, l.p+2)
				} else if d == 41 && l.code(l.p+2) == 41 {
					l.emitFlat(T_DOUBLECIRCLEEND, l.p+3)
				} else {
					l.emitFlat(T_PE, l.p+1)
				}
			case 124:
				l.pop()
				l.emitFlat(T_PIPE, l.p+1)
			case 125:
				l.pop()
				l.emitFlat(T_DIAMOND_STOP, l.p+1)
			case 37:
				// Jison drops the lone `%` from Mermaid's text pattern, so only a comment may start with one.
				if e := l.commentEnd(l.p); e >= 0 {
					l.advance(e)
				} else {
					ok = false
				}
			default:
				q := l.p + 1
				for q < l.n {
					ch := l.src[q]
					if ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 124 || ch == 34 || ch == 37 {
						break
					}
					q++
				}
				l.emit(T_TEXT, q)
			}

		case S_ellipseText:
			d := l.code(l.p + 1)
			var e int
			switch {
			case c == 34:
				l.quote()
			case c == 37 && func() bool { e = l.commentEnd(l.p); return e >= 0 }():
				l.advance(e)
			case (c == 45 || c == 47 || c == 41) && d == 41:
				l.pop()
				l.emitFlat(T_ELLIPSE_END, l.p+2)
			case c == 40 || c == 91 || c == 123:
				l.opener(c)
			case c == 41 || c == 93 || c == 125:
				ok = false
			default:
				q := l.p + 1
				for q < l.n {
					ch := l.src[q]
					if ch == 34 || ch == 40 || ch == 41 || ch == 91 || ch == 93 || ch == 123 || ch == 125 {
						break
					}
					if (ch == 45 || ch == 47) && l.code(q+1) == 41 {
						break
					}
					if ch == 37 && l.code(q+1) == 37 && l.code(q+2) != 123 {
						break
					}
					q++
				}
				l.emitChars(T_TEXT, q)
			}

		case S_trapText:
			d := l.code(l.p + 1)
			if c == 34 {
				l.quote()
				break
			}
			switch c {
			case 40:
				if d == 45 || d == 91 || (d == 40 && l.code(l.p+2) == 40) {
					l.opener(c)
				} else if d == 93 {
					l.pop()
					l.emitFlat(T_TRAPEND, l.p+2)
				} else {
					l.opener(c)
				}
			case 91, 123:
				l.opener(c)
			case 92:
				if d == 93 {
					l.pop()
					l.emitFlat(T_TRAPEND, l.p+2)
				} else {
					l.emitFlat(T_TEXT, l.p+1)
				}
			case 47:
				if d == 93 {
					l.pop()
					l.emitFlat(T_INVTRAPEND, l.p+2)
				} else {
					l.emitFlat(T_TEXT, l.p+1)
				}
			case 93, 41:
				if d == 93 {
					l.pop()
					l.emitFlat(T_TRAPEND, l.p+2)
				} else {
					ok = false
				}
			case 125:
				ok = false
			case 37:
				if e := l.commentEnd(l.p); e >= 0 {
					l.advance(e)
				} else {
					ok = false
				}
			default:
				if (c == 63 || c == 61) && d == 93 {
					l.pop()
					l.emitFlat(T_TRAPEND, l.p+2)
					break
				}
				q := l.p + 1
				for q < l.n {
					ch := l.src[q]
					if ch == 92 || ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 47 || ch == 37 {
						break
					}
					q++
				}
				l.emit(T_TEXT, q)
			}

		case S_edgeText:
			if c == 34 {
				l.quote()
				break
			}
			if e := l.arrowEnd(l.p); e >= 0 {
				l.pop()
				l.emit(T_LINK, e)
				break
			}
			if c == 45 && l.code(l.p+1) == 45 {
				ok = false
				break
			}
			q := l.p + l.size(l.p)
			for q < l.n {
				ch := l.code(q)
				if ch == 34 {
					break
				}
				if ch == 45 {
					if l.code(q+1) == 45 {
						break
					}
				} else if l.isSpace(ch) && l.arrowEnd(q) >= 0 {
					break
				}
				q += l.size(q)
			}
			l.emitChars(T_EDGE_TEXT, q)

		case S_click:
			if c == 34 {
				l.quote()
			} else if l.isSpace(c) {
				l.pop()
				l.advance(l.p + l.size(l.p))
			} else {
				q := l.p + l.size(l.p)
				for q < l.n && !l.isSpace(l.code(q)) {
					q += l.size(q)
				}
				l.emitFlat(T_CLICK, q)
			}

		case S_callbackname:
			if c == 40 {
				e := l.at(RE_EMPTY_CALL, l.p)
				l.pop()
				if e >= 0 {
					l.advance(e)
				} else {
					l.push(S_callbackargs)
					l.advance(l.p + 1)
				}
			} else {
				e := strings.IndexByte(l.src[l.p:], '(')
				if e == -1 {
					e = l.n
				} else {
					e += l.p
				}
				l.emit(T_CALLBACKNAME, e)
			}

		case S_callbackargs:
			if c == 41 {
				l.pop()
				l.advance(l.p + 1)
			} else {
				e := strings.IndexByte(l.src[l.p:], ')')
				if e == -1 {
					e = l.n
				} else {
					e += l.p
				}
				l.emit(T_CALLBACKARGS, e)
			}

		case S_shapeData:
			switch c {
			case 34:
				l.push(S_shapeDataStr)
				l.emitFlat(T_SHAPE_DATA, l.p+1)
			case 125:
				l.pop()
				l.advance(l.p + 1)
			case 94:
				ok = false
			default:
				q := l.p + 1
				for q < l.n && l.src[q] != 125 && l.src[q] != 94 && l.src[q] != 34 {
					q++
				}
				l.emit(T_SHAPE_DATA, q)
			}

		case S_shapeDataStr:
			if c == 34 {
				l.pop()
				l.emitFlat(T_SHAPE_DATA, l.p+1)
			} else {
				e := strings.IndexByte(l.src[l.p:], '"')
				if e == -1 {
					e = l.n
				} else {
					e += l.p
				}
				l.emitText(T_SHAPE_DATA, RE_BR.ReplaceAllString(l.src[l.p:e], "<br/>"), e)
			}
		}

		if !ok {
			l.types = append(l.types, T_ERROR)
			l.texts = append(l.texts, "")
			l.starts = append(l.starts, l.p)
			l.locs = append(l.locs, l.line, l.col, l.line, l.col)
			return Tokens{l.types, l.texts, l.starts, l.locs}
		}
	}

	l.types = append(l.types, T_END)
	l.texts = append(l.texts, "")
	l.starts = append(l.starts, l.n)
	l.locs = append(l.locs, l.line, l.col, l.line, l.col)
	return Tokens{l.types, l.texts, l.starts, l.locs}
}
