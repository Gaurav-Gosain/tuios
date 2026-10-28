// Ported from pele src/diagrams/flowchart/lexer.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele reads UTF-16 code units with charCodeAt. The port reads the UTF-8 bytes:
// code() decodes the character at a byte offset, and where pele steps one unit
// past a character that may not be ASCII, the port steps the whole character.
// Positions are byte offsets; types.Locate turns them into lines and columns.

package flowchart

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
	S_thickEdgeText
	S_dottedEdgeText
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
	RE_LINK         = sticky(sp + `*[xo<]?--+[-xo>]` + sp + `*`)
	RE_THICK_LINK   = sticky(sp + `*[xo<]?==+[=xo>]` + sp + `*`)
	RE_THICK_START  = sticky(sp + `*[xo<]?==` + sp + `*`)
	RE_DOTTED_LINK  = sticky(sp + `*[xo<]?-?\.+-[xo>]?` + sp + `*`)
	RE_DOTTED_START = sticky(sp + `*[xo<]?-\.` + sp + `*`)
	RE_INVIS_LINK   = sticky(sp + `*~~~+` + sp + `*`)
	RE_EMPTY_CALL   = sticky(`\(` + sp + `*\)`)
	RE_BR           = lazyre.New(`\n` + sp + `*`)
)

// skipJSSpace returns where the run of JavaScript whitespace from q ends.
func skipJSSpace(src string, q int) int {
	for q < len(src) {
		r, size := utf8.DecodeRuneInString(src[q:])
		if !js.IsSpace(r) {
			break
		}
		q += size
	}
	return q
}

// linkEnd is RE_LINK, /\s*[xo<]?--+[-xo>]\s*/y, at p: where the link ends, or -1.
func linkEnd(src string, p int) int {
	q := skipJSSpace(src, p)
	if q < len(src) && (src[q] == 'x' || src[q] == 'o' || src[q] == '<') {
		q++
	}
	j := q
	for j < len(src) && src[j] == '-' {
		j++
	}
	switch dashes := j - q; {
	case dashes >= 2 && j < len(src) && (src[j] == 'x' || src[j] == 'o' || src[j] == '>'):
		j++
	case dashes >= 3:
		// The last dash is the [-xo>].
	default:
		return -1
	}
	return skipJSSpace(src, j)
}

// startLinkEnd is RE_START_LINK, /\s*[xo<]?--\s*/y, at p: where the start of
// a link with text ends, or -1.
func startLinkEnd(src string, p int) int {
	q := skipJSSpace(src, p)
	if q < len(src) && (src[q] == 'x' || src[q] == 'o' || src[q] == '<') {
		q++
	}
	if !strings.HasPrefix(src[q:], "--") {
		return -1
	}
	return skipJSSpace(src, q+2)
}

// newlineRun is /(?:\r?\n)+/y at p: where the run of line ends from p ends, or
// -1 if there is none.
func newlineRun(src string, p int) int {
	q := p
	for q < len(src) {
		if src[q] == '\n' {
			q++
		} else if src[q] == '\r' && q+1 < len(src) && src[q+1] == '\n' {
			q += 2
		} else {
			break
		}
	}
	if q == p {
		return -1
	}
	return q
}

// spaceRun is /[^\S\n\r]+/y at p: where the run of whitespace other than \n
// and \r from p ends, or -1 if there is none.
func spaceRun(src string, p int) int {
	q := p
	for q < len(src) {
		r, size := utf8.DecodeRuneInString(src[q:])
		if r == '\n' || r == '\r' || !js.IsSpace(r) {
			break
		}
		q += size
	}
	if q == p {
		return -1
	}
	return q
}

// lexer is the state of one run of tokenize: the tokens so far, the mode stack and the read position.
type lexer struct {
	src    string
	n      int
	types  []int
	texts  []string
	starts []int
	stack  []int
	state  int
	p      int

	firstGraph bool
	dirNext    int
	atNext     int
	lineEnd    int
	dirFailed  int
	runEnd     int
	runAt      int
	wsFrom     int
	wsTo       int
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

//go:noinline
func (l *lexer) isSpace(c rune) bool { return util.IsSpace(c) }

//go:noinline
func (l *lexer) emit(typ int, s, e int) {
	l.types = append(l.types, typ)
	l.texts = append(l.texts, l.src[s:e])
	l.starts = append(l.starts, s)
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

func (l *lexer) skipSpace(j int) int {
	for j < l.n && l.isSpace(l.code(j)) {
		j += l.size(j)
	}
	return j
}

// First character after any run of whitespace starting at q, skipping one optional x, o or <.
func (l *lexer) linkChar(q int) rune {
	j := q
	if q >= l.wsFrom && q <= l.wsTo {
		j = l.wsTo
	} else if l.isSpace(l.code(q)) {
		j = l.skipSpace(j)
		l.wsFrom = q
		l.wsTo = j
	}
	d := l.code(j)
	if d == 120 || d == 111 || d == 60 {
		return l.code(j + 1)
	}
	return d
}

func (l *lexer) invisAhead(q int) bool {
	j := q
	if q >= l.wsFrom && q <= l.wsTo {
		j = l.wsTo
	} else if l.isSpace(l.code(q)) {
		j = l.skipSpace(j)
		l.wsFrom = q
		l.wsTo = j
	}
	return l.code(j) == 126
}

func (l *lexer) quote() {
	if l.code(l.p+1) == 96 {
		l.push(S_md_string)
		l.p += 2
	} else {
		l.push(S_string)
		l.p++
	}
}

func (l *lexer) invisLink() bool {
	if !l.invisAhead(l.p) {
		return false
	}
	e := l.at(RE_INVIS_LINK, l.p)
	if e < 0 {
		return false
	}
	l.emit(T_LINK, l.p, e)
	return true
}

func (l *lexer) opener(c rune) bool {
	d := l.code(l.p + 1)
	if c == 40 {
		if d == 45 {
			l.push(S_ellipseText)
			l.emit(T_ELLIPSE_START, l.p, l.p+2)
		} else if d == 91 {
			l.push(S_text)
			l.emit(T_STADIUMSTART, l.p, l.p+2)
		} else if d == 40 && l.code(l.p+2) == 40 {
			l.push(S_text)
			l.emit(T_DOUBLECIRCLESTART, l.p, l.p+3)
		} else {
			l.push(S_text)
			l.emit(T_PS, l.p, l.p+1)
		}
		return true
	}
	if c == 91 {
		if d == 91 {
			l.push(S_text)
			l.emit(T_SUBROUTINESTART, l.p, l.p+2)
		} else if d == 40 {
			l.push(S_text)
			l.emit(T_CYLINDERSTART, l.p, l.p+2)
		} else if d == 47 {
			l.push(S_trapText)
			l.emit(T_TRAPSTART, l.p, l.p+2)
		} else if d == 92 {
			l.push(S_trapText)
			l.emit(T_INVTRAPSTART, l.p, l.p+2)
		} else {
			l.push(S_text)
			l.emit(T_SQS, l.p, l.p+1)
		}
		return true
	}
	if c == 124 {
		l.push(S_text)
		l.emit(T_PIPE, l.p, l.p+1)
		return true
	}
	if c == 123 {
		l.push(S_text)
		l.emit(T_DIAMOND_START, l.p, l.p+1)
		return true
	}
	return false
}

//go:noinline
func (l *lexer) graph(length int) {
	if l.firstGraph {
		l.firstGraph = false
		l.push(S_dir)
	}
	l.emit(T_GRAPH, l.p, l.p+length)
}

func (l *lexer) nodeString() bool {
	q := l.p
	for q < l.n {
		ch := l.code(q)
		if ch >= 0 && ch < 128 && NODE_CHAR[ch] == 1 {
			q++
		} else if ch == 45 && q+1 < l.n {
			d := l.code(q + 1)
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
	l.emit(T_NODE_STRING, l.p, q)
	return true
}

func (l *lexer) link(c rune) bool {
	d := l.linkChar(l.p)
	var e int
	if d == 45 {
		if e = linkEnd(l.src, l.p); e >= 0 {
			l.emit(T_LINK, l.p, e)
			return true
		}
		if e = startLinkEnd(l.src, l.p); e >= 0 {
			l.push(S_edgeText)
			l.emit(T_START_LINK, l.p, e)
			return true
		}
		if e = l.at(RE_DOTTED_LINK, l.p); e >= 0 {
			l.emit(T_LINK, l.p, e)
			return true
		}
		if e = l.at(RE_DOTTED_START, l.p); e >= 0 {
			l.push(S_dottedEdgeText)
			l.emit(T_START_LINK, l.p, e)
			return true
		}
	} else if d == 61 {
		if e = l.at(RE_THICK_LINK, l.p); e >= 0 {
			l.emit(T_LINK, l.p, e)
			return true
		}
		if e = l.at(RE_THICK_START, l.p); e >= 0 {
			l.push(S_thickEdgeText)
			l.emit(T_START_LINK, l.p, e)
			return true
		}
	} else if d == 46 {
		if e = l.at(RE_DOTTED_LINK, l.p); e >= 0 {
			l.emit(T_LINK, l.p, e)
			return true
		}
	}
	return c != 45 && c != 61 && c != 46 && l.invisLink()
}

func (l *lexer) initial(c rune) bool {
	var e int
	switch c {
	case 97:
		if strings.HasPrefix(l.src[l.p:], "acc") {
			if e = l.at(RE_ACC_TITLE, l.p); e >= 0 {
				l.push(S_acc_title)
				l.emit(T_acc_title, l.p, e)
				return true
			}
			if e = l.at(RE_ACC_DESCR, l.p); e >= 0 {
				l.push(S_acc_descr)
				l.emit(T_acc_descr, l.p, e)
				return true
			}
			if e = l.at(RE_ACC_DESCR_ML, l.p); e >= 0 {
				l.push(S_acc_descr_multiline)
				l.p = e
				return true
			}
		}
	case 64:
		if l.code(l.p+1) == 123 {
			l.push(S_shapeData)
			l.types = append(l.types, T_SHAPE_DATA)
			l.texts = append(l.texts, "")
			l.starts = append(l.starts, l.p)
			l.p += 2
			return true
		}
	case 99:
		if e = l.at(RE_CALL, l.p); e >= 0 {
			l.push(S_callbackname)
			l.p = e
			return true
		}
		if l.kw("classDef") {
			l.emit(T_CLASSDEF, l.p, l.p+8)
			return true
		}
		if l.kw("class") {
			l.emit(T_CLASS, l.p, l.p+5)
			return true
		}
		if e = l.at(RE_CLICK, l.p); e >= 0 {
			l.push(S_click)
			l.p = e
			return true
		}
	case 34:
		l.quote()
		return true
	case 115:
		if l.kw("style") {
			l.emit(T_STYLE, l.p, l.p+5)
			return true
		}
		if l.kw("swimlane-beta") {
			l.graph(13)
			return true
		}
		if l.kw("subgraph") {
			l.emit(T_subgraph, l.p, l.p+8)
			return true
		}
	case 100:
		if l.kw("default") {
			l.emit(T_DEFAULT, l.p, l.p+7)
			return true
		}
	case 108:
		if l.kw("linkStyle") {
			l.emit(T_LINKSTYLE, l.p, l.p+9)
			return true
		}
	case 105:
		if l.kw("interpolate") {
			l.emit(T_INTERPOLATE, l.p, l.p+11)
			return true
		}
	case 104:
		if strings.HasPrefix(l.src[l.p:], "href") && l.isSpace(l.code(l.p+4)) {
			l.emit(T_HREF, l.p, l.p+5)
			return true
		}
	case 102:
		if l.kw("flowchart-elk") {
			l.graph(13)
			return true
		}
		if l.kw("flowchart") {
			l.graph(9)
			return true
		}
	case 103:
		if l.kw("graph") {
			l.graph(5)
			return true
		}
	case 101:
		if l.kw("end") {
			e = l.skipSpace(l.p + 3)
			l.emit(T_end, l.p, e)
			return true
		}
	case 95:
		if l.kw("_self") {
			l.emit(T_LINK_TARGET, l.p, l.p+5)
			return true
		}
		if l.kw("_blank") {
			l.emit(T_LINK_TARGET, l.p, l.p+6)
			return true
		}
		if l.kw("_parent") {
			l.emit(T_LINK_TARGET, l.p, l.p+7)
			return true
		}
		if l.kw("_top") {
			l.emit(T_LINK_TARGET, l.p, l.p+4)
			return true
		}
	}

	if l.dirNext != -1 && l.p >= l.dirFailed {
		if l.dirNext < l.p {
			l.dirNext = indexFrom(l.src, "direction", l.p)
		}
		if l.dirNext != -1 {
			if l.lineEnd < l.p {
				l.lineEnd = l.n
				for q := l.p; q < l.n; q += l.size(q) {
					if util.IsLineEnd(l.code(q)) {
						l.lineEnd = q
						break
					}
				}
			}
			if l.dirNext < l.lineEnd {
				for i := 0; i < 5; i++ {
					if e = l.at(RE_DIRECTION[i], l.p); e >= 0 {
						l.emit(T_direction_tb+i, l.p, e)
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
				l.atNext = indexFrom(l.src, "@", l.p)
			}
			q := l.p
			for q < l.n {
				ch := l.code(q)
				if ch == 34 || l.isSpace(ch) {
					break
				}
				q++
			}
			l.runEnd = q
			l.runAt = -1
			if l.atNext != -1 && l.atNext < q {
				for k := min(q, l.n-1) - 1; k >= l.atNext; k-- {
					if l.code(k) != 64 {
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
			l.emit(T_LINK_ID, l.p, l.runAt+1)
			return true
		}
	}

	if c >= 48 && c <= 57 {
		q := l.p + 1
		for q < l.n {
			ch := l.code(q)
			if ch < 48 || ch > 57 {
				break
			}
			q++
		}
		l.emit(T_NUM, l.p, q)
		return true
	}

	switch c {
	case 35:
		l.emit(T_BRKT, l.p, l.p+1)
		return true
	case 58:
		if l.code(l.p+1) == 58 && l.code(l.p+2) == 58 {
			l.emit(T_STYLE_SEPARATOR, l.p, l.p+3)
		} else {
			l.emit(T_COLON, l.p, l.p+1)
		}
		return true
	case 38:
		l.emit(T_AMP, l.p, l.p+1)
		return true
	case 59:
		l.emit(T_SEMI, l.p, l.p+1)
		return true
	case 44:
		l.emit(T_COMMA, l.p, l.p+1)
		return true
	case 42:
		l.emit(T_MULT, l.p, l.p+1)
		return true
	case 120, 111:
		return l.link(c) || l.nodeString()
	case 60:
		if l.link(c) {
			return true
		}
		l.emit(T_TAGSTART, l.p, l.p+1)
		return true
	case 45:
		if l.link(c) || l.nodeString() {
			return true
		}
		l.emit(T_MINUS, l.p, l.p+1)
		return true
	case 61:
		return l.link(c)
	case 46:
		return l.link(c) || l.nodeString()
	case 126:
		return l.invisLink()
	case 40:
		return l.opener(c)
	case 91:
		if l.code(l.p+1) == 124 {
			l.emit(T_VERTEX_WITH_PROPS_START, l.p, l.p+2)
			return true
		}
		return l.opener(c)
	case 124, 123:
		return l.opener(c)
	case 62:
		l.push(S_text)
		l.emit(T_TAGEND, l.p, l.p+1)
		return true
	case 94:
		l.emit(T_UP, l.p, l.p+1)
		return true
	case 92:
		if l.code(l.p+1) == 124 {
			l.emit(T_SEP, l.p, l.p+2)
			return true
		}
		return l.nodeString()
	case 118:
		if !util.IsWord(l.code(l.p + 1)) {
			l.emit(T_DOWN, l.p, l.p+1)
			return true
		}
		return l.nodeString()
	}

	if l.isSpace(c) {
		if l.link(c) {
			return true
		}
		if e = newlineRun(l.src, l.p); e >= 0 {
			l.emit(T_NEWLINE, l.p, e)
			return true
		}
		if e = spaceRun(l.src, l.p); e >= 0 {
			l.emit(T_SPACE, l.p, e)
			return true
		}
		return false
	}

	if c < 128 {
		return l.nodeString()
	}
	if util.IsUnicodeLetter(c) {
		l.emit(T_UNICODE_TEXT, l.p, l.p+l.size(l.p))
		return true
	}
	return false
}

func (l *lexer) edgeText(c rune, linkRe *lazyre.Regexp, a, b rune) bool {
	if c == 34 {
		l.quote()
		return true
	}
	d := l.linkChar(l.p)
	if d == a || d == b {
		e := l.at(linkRe, l.p)
		if e >= 0 {
			l.pop()
			l.emit(T_LINK, l.p, e)
			return true
		}
	}
	if c == a && (a != 45 || l.code(l.p+1) == 45) {
		return false
	}
	q := l.p + l.size(l.p)
	for q < l.n {
		ch := l.code(q)
		if ch == 34 {
			break
		}
		if ch == a {
			if a != 45 || l.code(q+1) == 45 {
				break
			}
		} else if ch == 120 || ch == 111 || ch == 60 || ch == b || l.isSpace(ch) {
			dd := l.linkChar(q)
			if (dd == a || dd == b) && l.at(linkRe, q) >= 0 {
				break
			}
		}
		q++
	}
	l.emit(T_EDGE_TEXT, l.p, q)
	return true
}

func tokenize(src string) Tokens {
	n := len(src)
	// About one token for every three bytes of a flowchart.
	guess := n/3 + 8
	l := &lexer{
		src:        src,
		n:          n,
		types:      make([]int, 0, guess),
		texts:      make([]string, 0, guess),
		starts:     make([]int, 0, guess),
		state:      S_INITIAL,
		firstGraph: true,
		dirNext:    strings.Index(src, "direction"),
		atNext:     strings.Index(src, "@"),
		lineEnd:    -1,
		runAt:      -1,
		wsFrom:     -1,
		wsTo:       -1,
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
				l.emit(typ, l.n, l.n)
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
					l.p += 2
				} else {
					l.pop()
					l.p++
				}
			} else {
				e := indexFrom(l.src, `"`, l.p)
				if e == -1 {
					e = l.n
				}
				l.emit(T_STR, l.p, e)
			}

		case S_md_string:
			if c == 96 {
				if l.code(l.p+1) == 34 {
					l.pop()
					l.p += 2
				} else {
					ok = false
				}
			} else if c == 34 {
				l.quote()
			} else {
				q := l.p + l.size(l.p)
				for q < l.n {
					ch := l.code(q)
					if ch == 96 || ch == 34 {
						break
					}
					q++
				}
				l.emit(T_MD_STR, l.p, q)
			}

		case S_acc_title, S_acc_descr:
			e := indexFrom(l.src, "\n", l.p)
			if e == -1 {
				e = l.n
			}
			typ := T_acc_descr_value
			if l.state == S_acc_title {
				typ = T_acc_title_value
			}
			l.pop()
			l.emit(typ, l.p, e)

		case S_acc_descr_multiline:
			if c == 125 {
				l.pop()
				l.p++
			} else {
				e := indexFrom(l.src, "}", l.p)
				if e == -1 {
					e = l.n
				}
				l.emit(T_acc_descr_multiline_value, l.p, e)
			}

		case S_dir:
			var e int
			if c == 34 {
				l.quote()
			} else if e = l.at(RE_NODIR, l.p); e >= 0 {
				l.pop()
				l.emit(T_NODIR, l.p, e)
			} else if e = l.at(RE_DIR, l.p); e >= 0 {
				l.pop()
				l.emit(T_DIR, l.p, e)
			} else if e = l.at(RE_DIR_SYM, l.p); e >= 0 {
				l.pop()
				l.emit(T_DIR, l.p, e)
			} else if e = l.at(RE_DIR_V, l.p); e >= 0 {
				l.pop()
				l.emit(T_DIR, l.p, e)
			} else {
				ok = l.invisLink() || l.opener(c)
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
					l.emit(T_STADIUMEND, l.p, l.p+2)
				} else if d == 93 {
					l.emit(T_SUBROUTINEEND, l.p, l.p+2)
				} else {
					l.emit(T_SQE, l.p, l.p+1)
				}
			case 41:
				l.pop()
				if d == 93 {
					l.emit(T_CYLINDEREND, l.p, l.p+2)
				} else if d == 41 && l.code(l.p+2) == 41 {
					l.emit(T_DOUBLECIRCLEEND, l.p, l.p+3)
				} else {
					l.emit(T_PE, l.p, l.p+1)
				}
			case 124:
				l.pop()
				l.emit(T_PIPE, l.p, l.p+1)
			case 125:
				l.pop()
				l.emit(T_DIAMOND_STOP, l.p, l.p+1)
			default:
				if (c == 126 || l.isSpace(c)) && l.invisLink() {
					break
				}
				q := l.p + l.size(l.p)
				for q < l.n {
					ch := l.code(q)
					if ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 124 || ch == 34 {
						break
					}
					q++
				}
				l.emit(T_TEXT, l.p, q)
			}

		case S_ellipseText:
			d := l.code(l.p + 1)
			if c == 34 {
				l.quote()
			} else if (c == 126 || l.isSpace(c)) && l.invisLink() {
				break
			} else if (c == 45 || c == 47 || c == 41) && d == 41 {
				l.pop()
				l.emit(T_ELLIPSE_END, l.p, l.p+2)
			} else if c == 40 || c == 91 || c == 123 {
				l.opener(c)
			} else if c == 41 || c == 93 || c == 125 {
				ok = false
			} else {
				q := l.p + l.size(l.p)
				for q < l.n {
					ch := l.code(q)
					if ch == 34 || ch == 40 || ch == 41 || ch == 91 || ch == 93 || ch == 123 || ch == 125 {
						break
					}
					if (ch == 45 || ch == 47) && l.code(q+1) == 41 {
						break
					}
					if (ch == 126 || l.isSpace(ch)) && l.invisAhead(q) && l.at(RE_INVIS_LINK, q) >= 0 {
						break
					}
					q++
				}
				l.emit(T_TEXT, l.p, q)
			}

		case S_trapText:
			d := l.code(l.p + 1)
			if c == 34 {
				l.quote()
				break
			}
			if (c == 126 || l.isSpace(c)) && l.invisLink() {
				break
			}
			switch c {
			case 40:
				if d == 45 || d == 91 || (d == 40 && l.code(l.p+2) == 40) {
					l.opener(c)
				} else if d == 93 {
					l.pop()
					l.emit(T_TRAPEND, l.p, l.p+2)
				} else {
					l.opener(c)
				}
			case 91, 123:
				l.opener(c)
			case 92:
				if d == 93 {
					l.pop()
					l.emit(T_TRAPEND, l.p, l.p+2)
				} else {
					l.emit(T_TEXT, l.p, l.p+1)
				}
			case 47:
				if d == 93 {
					l.pop()
					l.emit(T_INVTRAPEND, l.p, l.p+2)
				} else {
					l.emit(T_TEXT, l.p, l.p+1)
				}
			case 93, 41:
				if d == 93 {
					l.pop()
					l.emit(T_TRAPEND, l.p, l.p+2)
				} else {
					ok = false
				}
			case 125:
				ok = false
			default:
				if (c == 63 || c == 61) && d == 93 {
					l.pop()
					l.emit(T_TRAPEND, l.p, l.p+2)
					break
				}
				q := l.p + l.size(l.p)
				for q < l.n {
					ch := l.code(q)
					if ch == 92 || ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 47 {
						break
					}
					q++
				}
				l.emit(T_TEXT, l.p, q)
			}

		case S_edgeText:
			ok = l.edgeText(c, RE_LINK, 45, -1)
		case S_thickEdgeText:
			ok = l.edgeText(c, RE_THICK_LINK, 61, -1)
		case S_dottedEdgeText:
			ok = l.edgeText(c, RE_DOTTED_LINK, 46, 45)

		case S_click:
			if c == 34 {
				l.quote()
			} else if l.isSpace(c) {
				l.pop()
				l.p += l.size(l.p)
			} else {
				q := l.p + l.size(l.p)
				for q < l.n && !l.isSpace(l.code(q)) {
					q++
				}
				l.emit(T_CLICK, l.p, q)
			}

		case S_callbackname:
			if c == 40 {
				e := l.at(RE_EMPTY_CALL, l.p)
				l.pop()
				if e >= 0 {
					l.p = e
				} else {
					l.push(S_callbackargs)
					l.p++
				}
			} else {
				e := indexFrom(l.src, "(", l.p)
				if e == -1 {
					e = l.n
				}
				l.emit(T_CALLBACKNAME, l.p, e)
			}

		case S_callbackargs:
			if c == 41 {
				l.pop()
				l.p++
			} else {
				e := indexFrom(l.src, ")", l.p)
				if e == -1 {
					e = l.n
				}
				l.emit(T_CALLBACKARGS, l.p, e)
			}

		case S_shapeData:
			if c == 34 {
				l.push(S_shapeDataStr)
				l.emit(T_SHAPE_DATA, l.p, l.p+1)
			} else if c == 125 {
				l.pop()
				l.p++
			} else if c == 94 {
				ok = false
			} else {
				q := l.p + l.size(l.p)
				for q < l.n {
					ch := l.code(q)
					if ch == 125 || ch == 94 || ch == 34 {
						break
					}
					q++
				}
				l.emit(T_SHAPE_DATA, l.p, q)
			}

		case S_shapeDataStr:
			if c == 34 {
				l.pop()
				l.emit(T_SHAPE_DATA, l.p, l.p+1)
			} else {
				e := indexFrom(l.src, `"`, l.p)
				if e == -1 {
					e = l.n
				}
				l.types = append(l.types, T_SHAPE_DATA)
				l.texts = append(l.texts, RE_BR.ReplaceAllString(l.src[l.p:e], "<br/>"))
				l.starts = append(l.starts, l.p)
				l.p = e
			}
		}

		if !ok {
			l.types = append(l.types, T_ERROR)
			l.texts = append(l.texts, "")
			l.starts = append(l.starts, l.p)
			return Tokens{l.types, l.texts, l.starts}
		}
	}

	l.types = append(l.types, T_END)
	l.texts = append(l.texts, "")
	l.starts = append(l.starts, l.n)
	return Tokens{l.types, l.texts, l.starts}
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
