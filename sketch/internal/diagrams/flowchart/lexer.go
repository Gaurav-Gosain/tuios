// Ported from pele src/diagrams/flowchart/lexer.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// pele reads UTF-16 code units with charCodeAt. The port reads the UTF-8 bytes:
// code() decodes the character at a byte offset, and where pele steps one unit
// past a character that may not be ASCII, the port steps the whole character.
// Positions are byte offsets; types.Locate turns them into lines and columns.

package flowchart

import (
	"regexp"
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
func sticky(pattern string) *regexp.Regexp { return regexp.MustCompile(`^(?:` + pattern + `)`) }

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
	RE_DIRECTION    = [5]*regexp.Regexp{
		sticky(js.Dot + `*direction` + sp + `+TB[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+BT[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+RL[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+LR[^\n]*`),
		sticky(js.Dot + `*direction` + sp + `+TD[^\n]*`),
	}
	RE_LINK         = sticky(sp + `*[xo<]?--+[-xo>]` + sp + `*`)
	RE_START_LINK   = sticky(sp + `*[xo<]?--` + sp + `*`)
	RE_THICK_LINK   = sticky(sp + `*[xo<]?==+[=xo>]` + sp + `*`)
	RE_THICK_START  = sticky(sp + `*[xo<]?==` + sp + `*`)
	RE_DOTTED_LINK  = sticky(sp + `*[xo<]?-?\.+-[xo>]?` + sp + `*`)
	RE_DOTTED_START = sticky(sp + `*[xo<]?-\.` + sp + `*`)
	RE_INVIS_LINK   = sticky(sp + `*~~~+` + sp + `*`)
	RE_NEWLINE      = sticky(`(?:\r?\n)+`)
	// [^\S\n\r]: whitespace other than \n and \r.
	RE_SPACE      = sticky(`[\t\v\f \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
	RE_EMPTY_CALL = sticky(`\(` + sp + `*\)`)
	RE_BR         = regexp.MustCompile(`\n` + sp + `*`)
)

func tokenize(src string) Tokens {
	n := len(src)
	var types []int
	var texts []string
	var starts []int
	var stack []int
	state := S_INITIAL
	p := 0
	firstGraph := true
	dirNext := strings.Index(src, "direction")
	atNext := strings.Index(src, "@")
	lineEnd := -1
	dirFailed := 0
	runEnd := 0
	runAt := -1
	wsFrom := -1
	wsTo := -1

	// code is the character at byte q, as charCodeAt would give it, or -1 past the end.
	// A byte inside a character gives 0xfffe, which no test matches.
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
			// A high surrogate, as charCodeAt gives for the first unit.
			return 0xd800
		}
		return r
	}
	// size is how many bytes the character at q takes: one step of pele's.
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
	at := func(re *regexp.Regexp, q int) int {
		if q > n {
			return -1
		}
		loc := re.FindStringIndex(src[q:])
		if loc == nil {
			return -1
		}
		return q + loc[1]
	}
	kw := func(word string) bool {
		return strings.HasPrefix(src[p:], word) && !util.IsWord(code(p+len(word)))
	}
	skipSpace := func(j int) int {
		for j < n && isSpace(code(j)) {
			j += size(j)
		}
		return j
	}

	// First character after any run of whitespace starting at q, skipping one optional x, o or <.
	linkChar := func(q int) rune {
		j := q
		if q >= wsFrom && q <= wsTo {
			j = wsTo
		} else if isSpace(code(q)) {
			j = skipSpace(j)
			wsFrom = q
			wsTo = j
		}
		d := code(j)
		if d == 120 || d == 111 || d == 60 {
			return code(j + 1)
		}
		return d
	}
	invisAhead := func(q int) bool {
		j := q
		if q >= wsFrom && q <= wsTo {
			j = wsTo
		} else if isSpace(code(q)) {
			j = skipSpace(j)
			wsFrom = q
			wsTo = j
		}
		return code(j) == 126
	}

	quote := func() {
		if code(p+1) == 96 {
			push(S_md_string)
			p += 2
		} else {
			push(S_string)
			p++
		}
	}

	invisLink := func() bool {
		if !invisAhead(p) {
			return false
		}
		e := at(RE_INVIS_LINK, p)
		if e < 0 {
			return false
		}
		emit(T_LINK, p, e)
		return true
	}

	opener := func(c rune) bool {
		d := code(p + 1)
		if c == 40 {
			if d == 45 {
				push(S_ellipseText)
				emit(T_ELLIPSE_START, p, p+2)
			} else if d == 91 {
				push(S_text)
				emit(T_STADIUMSTART, p, p+2)
			} else if d == 40 && code(p+2) == 40 {
				push(S_text)
				emit(T_DOUBLECIRCLESTART, p, p+3)
			} else {
				push(S_text)
				emit(T_PS, p, p+1)
			}
			return true
		}
		if c == 91 {
			if d == 91 {
				push(S_text)
				emit(T_SUBROUTINESTART, p, p+2)
			} else if d == 40 {
				push(S_text)
				emit(T_CYLINDERSTART, p, p+2)
			} else if d == 47 {
				push(S_trapText)
				emit(T_TRAPSTART, p, p+2)
			} else if d == 92 {
				push(S_trapText)
				emit(T_INVTRAPSTART, p, p+2)
			} else {
				push(S_text)
				emit(T_SQS, p, p+1)
			}
			return true
		}
		if c == 124 {
			push(S_text)
			emit(T_PIPE, p, p+1)
			return true
		}
		if c == 123 {
			push(S_text)
			emit(T_DIAMOND_START, p, p+1)
			return true
		}
		return false
	}

	graph := func(length int) {
		if firstGraph {
			firstGraph = false
			push(S_dir)
		}
		emit(T_GRAPH, p, p+length)
	}

	nodeString := func() bool {
		q := p
		for q < n {
			ch := code(q)
			if ch >= 0 && ch < 128 && NODE_CHAR[ch] == 1 {
				q++
			} else if ch == 45 && q+1 < n {
				d := code(q + 1)
				if d == 62 || d == 45 || d == 46 {
					break
				}
				q++
			} else {
				break
			}
		}
		if q == p {
			return false
		}
		emit(T_NODE_STRING, p, q)
		return true
	}

	link := func(c rune) bool {
		d := linkChar(p)
		var e int
		if d == 45 {
			if e = at(RE_LINK, p); e >= 0 {
				emit(T_LINK, p, e)
				return true
			}
			if e = at(RE_START_LINK, p); e >= 0 {
				push(S_edgeText)
				emit(T_START_LINK, p, e)
				return true
			}
			if e = at(RE_DOTTED_LINK, p); e >= 0 {
				emit(T_LINK, p, e)
				return true
			}
			if e = at(RE_DOTTED_START, p); e >= 0 {
				push(S_dottedEdgeText)
				emit(T_START_LINK, p, e)
				return true
			}
		} else if d == 61 {
			if e = at(RE_THICK_LINK, p); e >= 0 {
				emit(T_LINK, p, e)
				return true
			}
			if e = at(RE_THICK_START, p); e >= 0 {
				push(S_thickEdgeText)
				emit(T_START_LINK, p, e)
				return true
			}
		} else if d == 46 {
			if e = at(RE_DOTTED_LINK, p); e >= 0 {
				emit(T_LINK, p, e)
				return true
			}
		}
		return c != 45 && c != 61 && c != 46 && invisLink()
	}

	initial := func(c rune) bool {
		var e int
		switch c {
		case 97:
			if strings.HasPrefix(src[p:], "acc") {
				if e = at(RE_ACC_TITLE, p); e >= 0 {
					push(S_acc_title)
					emit(T_acc_title, p, e)
					return true
				}
				if e = at(RE_ACC_DESCR, p); e >= 0 {
					push(S_acc_descr)
					emit(T_acc_descr, p, e)
					return true
				}
				if e = at(RE_ACC_DESCR_ML, p); e >= 0 {
					push(S_acc_descr_multiline)
					p = e
					return true
				}
			}
		case 64:
			if code(p+1) == 123 {
				push(S_shapeData)
				types = append(types, T_SHAPE_DATA)
				texts = append(texts, "")
				starts = append(starts, p)
				p += 2
				return true
			}
		case 99:
			if e = at(RE_CALL, p); e >= 0 {
				push(S_callbackname)
				p = e
				return true
			}
			if kw("classDef") {
				emit(T_CLASSDEF, p, p+8)
				return true
			}
			if kw("class") {
				emit(T_CLASS, p, p+5)
				return true
			}
			if e = at(RE_CLICK, p); e >= 0 {
				push(S_click)
				p = e
				return true
			}
		case 34:
			quote()
			return true
		case 115:
			if kw("style") {
				emit(T_STYLE, p, p+5)
				return true
			}
			if kw("swimlane-beta") {
				graph(13)
				return true
			}
			if kw("subgraph") {
				emit(T_subgraph, p, p+8)
				return true
			}
		case 100:
			if kw("default") {
				emit(T_DEFAULT, p, p+7)
				return true
			}
		case 108:
			if kw("linkStyle") {
				emit(T_LINKSTYLE, p, p+9)
				return true
			}
		case 105:
			if kw("interpolate") {
				emit(T_INTERPOLATE, p, p+11)
				return true
			}
		case 104:
			if strings.HasPrefix(src[p:], "href") && isSpace(code(p+4)) {
				emit(T_HREF, p, p+5)
				return true
			}
		case 102:
			if kw("flowchart-elk") {
				graph(13)
				return true
			}
			if kw("flowchart") {
				graph(9)
				return true
			}
		case 103:
			if kw("graph") {
				graph(5)
				return true
			}
		case 101:
			if kw("end") {
				e = skipSpace(p + 3)
				emit(T_end, p, e)
				return true
			}
		case 95:
			if kw("_self") {
				emit(T_LINK_TARGET, p, p+5)
				return true
			}
			if kw("_blank") {
				emit(T_LINK_TARGET, p, p+6)
				return true
			}
			if kw("_parent") {
				emit(T_LINK_TARGET, p, p+7)
				return true
			}
			if kw("_top") {
				emit(T_LINK_TARGET, p, p+4)
				return true
			}
		}

		if dirNext != -1 && p >= dirFailed {
			if dirNext < p {
				dirNext = indexFrom(src, "direction", p)
			}
			if dirNext != -1 {
				if lineEnd < p {
					lineEnd = n
					for q := p; q < n; q += size(q) {
						if util.IsLineEnd(code(q)) {
							lineEnd = q
							break
						}
					}
				}
				if dirNext < lineEnd {
					for i := 0; i < 5; i++ {
						if e = at(RE_DIRECTION[i], p); e >= 0 {
							emit(T_direction_tb+i, p, e)
							return true
						}
					}
					// No direction statement starts anywhere in the rest of this line either.
					dirFailed = lineEnd
				}
			}
		}

		// A link id is a run of non-space characters up to its last `@` that is not followed by `{` or `"`.
		if atNext != -1 && c != 34 && !isSpace(c) {
			if p >= runEnd {
				if atNext < p {
					atNext = indexFrom(src, "@", p)
				}
				q := p
				for q < n {
					ch := code(q)
					if ch == 34 || isSpace(ch) {
						break
					}
					q++
				}
				runEnd = q
				runAt = -1
				if atNext != -1 && atNext < q {
					for k := min(q, n-1) - 1; k >= atNext; k-- {
						if code(k) != 64 {
							continue
						}
						after := code(k + 1)
						if after != 123 && after != 34 {
							runAt = k
							break
						}
					}
				}
			}
			if runAt > p {
				emit(T_LINK_ID, p, runAt+1)
				return true
			}
		}

		if c >= 48 && c <= 57 {
			q := p + 1
			for q < n {
				ch := code(q)
				if ch < 48 || ch > 57 {
					break
				}
				q++
			}
			emit(T_NUM, p, q)
			return true
		}

		switch c {
		case 35:
			emit(T_BRKT, p, p+1)
			return true
		case 58:
			if code(p+1) == 58 && code(p+2) == 58 {
				emit(T_STYLE_SEPARATOR, p, p+3)
			} else {
				emit(T_COLON, p, p+1)
			}
			return true
		case 38:
			emit(T_AMP, p, p+1)
			return true
		case 59:
			emit(T_SEMI, p, p+1)
			return true
		case 44:
			emit(T_COMMA, p, p+1)
			return true
		case 42:
			emit(T_MULT, p, p+1)
			return true
		case 120, 111:
			return link(c) || nodeString()
		case 60:
			if link(c) {
				return true
			}
			emit(T_TAGSTART, p, p+1)
			return true
		case 45:
			if link(c) || nodeString() {
				return true
			}
			emit(T_MINUS, p, p+1)
			return true
		case 61:
			return link(c)
		case 46:
			return link(c) || nodeString()
		case 126:
			return invisLink()
		case 40:
			return opener(c)
		case 91:
			if code(p+1) == 124 {
				emit(T_VERTEX_WITH_PROPS_START, p, p+2)
				return true
			}
			return opener(c)
		case 124, 123:
			return opener(c)
		case 62:
			push(S_text)
			emit(T_TAGEND, p, p+1)
			return true
		case 94:
			emit(T_UP, p, p+1)
			return true
		case 92:
			if code(p+1) == 124 {
				emit(T_SEP, p, p+2)
				return true
			}
			return nodeString()
		case 118:
			if !util.IsWord(code(p + 1)) {
				emit(T_DOWN, p, p+1)
				return true
			}
			return nodeString()
		}

		if isSpace(c) {
			if link(c) {
				return true
			}
			if e = at(RE_NEWLINE, p); e >= 0 {
				emit(T_NEWLINE, p, e)
				return true
			}
			if e = at(RE_SPACE, p); e >= 0 {
				emit(T_SPACE, p, e)
				return true
			}
			return false
		}

		if c < 128 {
			return nodeString()
		}
		if util.IsUnicodeLetter(c) {
			emit(T_UNICODE_TEXT, p, p+size(p))
			return true
		}
		return false
	}

	edgeText := func(c rune, linkRe *regexp.Regexp, a, b rune) bool {
		if c == 34 {
			quote()
			return true
		}
		d := linkChar(p)
		if d == a || d == b {
			e := at(linkRe, p)
			if e >= 0 {
				pop()
				emit(T_LINK, p, e)
				return true
			}
		}
		if c == a && (a != 45 || code(p+1) == 45) {
			return false
		}
		q := p + size(p)
		for q < n {
			ch := code(q)
			if ch == 34 {
				break
			}
			if ch == a {
				if a != 45 || code(q+1) == 45 {
					break
				}
			} else if ch == 120 || ch == 111 || ch == 60 || ch == b || isSpace(ch) {
				dd := linkChar(q)
				if (dd == a || dd == b) && at(linkRe, q) >= 0 {
					break
				}
			}
			q++
		}
		emit(T_EDGE_TEXT, p, q)
		return true
	}

	for {
		if p >= n {
			typ := -1
			switch state {
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
				emit(typ, n, n)
			}
			break
		}

		c := code(p)
		ok := true

		switch state {
		case S_INITIAL:
			ok = initial(c)

		case S_string:
			if c == 34 {
				if code(p+1) == 96 {
					push(S_md_string)
					p += 2
				} else {
					pop()
					p++
				}
			} else {
				e := indexFrom(src, `"`, p)
				if e == -1 {
					e = n
				}
				emit(T_STR, p, e)
			}

		case S_md_string:
			if c == 96 {
				if code(p+1) == 34 {
					pop()
					p += 2
				} else {
					ok = false
				}
			} else if c == 34 {
				quote()
			} else {
				q := p + size(p)
				for q < n {
					ch := code(q)
					if ch == 96 || ch == 34 {
						break
					}
					q++
				}
				emit(T_MD_STR, p, q)
			}

		case S_acc_title, S_acc_descr:
			e := indexFrom(src, "\n", p)
			if e == -1 {
				e = n
			}
			typ := T_acc_descr_value
			if state == S_acc_title {
				typ = T_acc_title_value
			}
			pop()
			emit(typ, p, e)

		case S_acc_descr_multiline:
			if c == 125 {
				pop()
				p++
			} else {
				e := indexFrom(src, "}", p)
				if e == -1 {
					e = n
				}
				emit(T_acc_descr_multiline_value, p, e)
			}

		case S_dir:
			var e int
			if c == 34 {
				quote()
			} else if e = at(RE_NODIR, p); e >= 0 {
				pop()
				emit(T_NODIR, p, e)
			} else if e = at(RE_DIR, p); e >= 0 {
				pop()
				emit(T_DIR, p, e)
			} else if e = at(RE_DIR_SYM, p); e >= 0 {
				pop()
				emit(T_DIR, p, e)
			} else if e = at(RE_DIR_V, p); e >= 0 {
				pop()
				emit(T_DIR, p, e)
			} else {
				ok = invisLink() || opener(c)
			}

		case S_text:
			d := code(p + 1)
			switch c {
			case 34:
				quote()
			case 40, 91, 123:
				opener(c)
			case 93:
				pop()
				if d == 41 {
					emit(T_STADIUMEND, p, p+2)
				} else if d == 93 {
					emit(T_SUBROUTINEEND, p, p+2)
				} else {
					emit(T_SQE, p, p+1)
				}
			case 41:
				pop()
				if d == 93 {
					emit(T_CYLINDEREND, p, p+2)
				} else if d == 41 && code(p+2) == 41 {
					emit(T_DOUBLECIRCLEEND, p, p+3)
				} else {
					emit(T_PE, p, p+1)
				}
			case 124:
				pop()
				emit(T_PIPE, p, p+1)
			case 125:
				pop()
				emit(T_DIAMOND_STOP, p, p+1)
			default:
				if (c == 126 || isSpace(c)) && invisLink() {
					break
				}
				q := p + size(p)
				for q < n {
					ch := code(q)
					if ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 124 || ch == 34 {
						break
					}
					q++
				}
				emit(T_TEXT, p, q)
			}

		case S_ellipseText:
			d := code(p + 1)
			if c == 34 {
				quote()
			} else if (c == 126 || isSpace(c)) && invisLink() {
				break
			} else if (c == 45 || c == 47 || c == 41) && d == 41 {
				pop()
				emit(T_ELLIPSE_END, p, p+2)
			} else if c == 40 || c == 91 || c == 123 {
				opener(c)
			} else if c == 41 || c == 93 || c == 125 {
				ok = false
			} else {
				q := p + size(p)
				for q < n {
					ch := code(q)
					if ch == 34 || ch == 40 || ch == 41 || ch == 91 || ch == 93 || ch == 123 || ch == 125 {
						break
					}
					if (ch == 45 || ch == 47) && code(q+1) == 41 {
						break
					}
					if (ch == 126 || isSpace(ch)) && invisAhead(q) && at(RE_INVIS_LINK, q) >= 0 {
						break
					}
					q++
				}
				emit(T_TEXT, p, q)
			}

		case S_trapText:
			d := code(p + 1)
			if c == 34 {
				quote()
				break
			}
			if (c == 126 || isSpace(c)) && invisLink() {
				break
			}
			switch c {
			case 40:
				if d == 45 || d == 91 || (d == 40 && code(p+2) == 40) {
					opener(c)
				} else if d == 93 {
					pop()
					emit(T_TRAPEND, p, p+2)
				} else {
					opener(c)
				}
			case 91, 123:
				opener(c)
			case 92:
				if d == 93 {
					pop()
					emit(T_TRAPEND, p, p+2)
				} else {
					emit(T_TEXT, p, p+1)
				}
			case 47:
				if d == 93 {
					pop()
					emit(T_INVTRAPEND, p, p+2)
				} else {
					emit(T_TEXT, p, p+1)
				}
			case 93, 41:
				if d == 93 {
					pop()
					emit(T_TRAPEND, p, p+2)
				} else {
					ok = false
				}
			case 125:
				ok = false
			default:
				if (c == 63 || c == 61) && d == 93 {
					pop()
					emit(T_TRAPEND, p, p+2)
					break
				}
				q := p + size(p)
				for q < n {
					ch := code(q)
					if ch == 92 || ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 47 {
						break
					}
					q++
				}
				emit(T_TEXT, p, q)
			}

		case S_edgeText:
			ok = edgeText(c, RE_LINK, 45, -1)
		case S_thickEdgeText:
			ok = edgeText(c, RE_THICK_LINK, 61, -1)
		case S_dottedEdgeText:
			ok = edgeText(c, RE_DOTTED_LINK, 46, 45)

		case S_click:
			if c == 34 {
				quote()
			} else if isSpace(c) {
				pop()
				p += size(p)
			} else {
				q := p + size(p)
				for q < n && !isSpace(code(q)) {
					q++
				}
				emit(T_CLICK, p, q)
			}

		case S_callbackname:
			if c == 40 {
				e := at(RE_EMPTY_CALL, p)
				pop()
				if e >= 0 {
					p = e
				} else {
					push(S_callbackargs)
					p++
				}
			} else {
				e := indexFrom(src, "(", p)
				if e == -1 {
					e = n
				}
				emit(T_CALLBACKNAME, p, e)
			}

		case S_callbackargs:
			if c == 41 {
				pop()
				p++
			} else {
				e := indexFrom(src, ")", p)
				if e == -1 {
					e = n
				}
				emit(T_CALLBACKARGS, p, e)
			}

		case S_shapeData:
			if c == 34 {
				push(S_shapeDataStr)
				emit(T_SHAPE_DATA, p, p+1)
			} else if c == 125 {
				pop()
				p++
			} else if c == 94 {
				ok = false
			} else {
				q := p + size(p)
				for q < n {
					ch := code(q)
					if ch == 125 || ch == 94 || ch == 34 {
						break
					}
					q++
				}
				emit(T_SHAPE_DATA, p, q)
			}

		case S_shapeDataStr:
			if c == 34 {
				pop()
				emit(T_SHAPE_DATA, p, p+1)
			} else {
				e := indexFrom(src, `"`, p)
				if e == -1 {
					e = n
				}
				types = append(types, T_SHAPE_DATA)
				texts = append(texts, RE_BR.ReplaceAllString(src[p:e], "<br/>"))
				starts = append(starts, p)
				p = e
			}
		}

		if !ok {
			types = append(types, T_ERROR)
			texts = append(texts, "")
			starts = append(starts, p)
			return Tokens{types, texts, starts}
		}
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
