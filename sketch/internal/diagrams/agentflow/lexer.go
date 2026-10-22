// Ported from pele src/diagrams/agentflow/lexer.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
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

func tokenize(src string) Tokens {
	n := len(src)
	// About one token for every three bytes.
	guess := n/3 + 8
	types := make([]int, 0, guess)
	texts := make([]string, 0, guess)
	starts := make([]int, 0, guess)
	locs := make([]int, 0, 4*guess)
	var stack []int
	state := S_INITIAL
	p := 0
	line := 1
	col := 0
	firstGraph := true
	dirNext := strings.Index(src, "direction")
	atNext := strings.Index(src, "@")
	lineEnd := -1
	dirFailed := 0
	runEnd := 0
	runAt := -1
	wsFrom := -1
	wsTo := -1
	blankFrom := -1
	blankTo := -1
	noLink := -1
	noArrow := -1

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
	// units is how many UTF-16 units src[a:b] holds.
	units := func(a, b int) int {
		u := 0
		for k := a; k < b; {
			if src[k] < utf8.RuneSelf {
				u++
				k++
				continue
			}
			r, s := utf8.DecodeRuneInString(src[k:b])
			if r > 0xffff {
				u += 2
			} else {
				u++
			}
			k += s
		}
		return u
	}
	isSpace := func(c rune) bool { return util.IsSpace(c) }

	// advance moves over one match. Jison counts \r\n, \r and \n as one line break each, and after a
	// break its column stops at the first U+2028 or U+2029.
	advance := func(e int) {
		brk := -1
		for k := p; k < e; k++ {
			ch := src[k]
			if ch == 10 {
				line++
				brk = k
			} else if ch == 13 {
				if k+1 < e && src[k+1] == 10 {
					k++
				}
				line++
				brk = k
			}
		}
		if brk < 0 {
			col += units(p, e)
		} else {
			k := brk + 1
			for k < e {
				r := code(k)
				if r == 0x2028 || r == 0x2029 {
					break
				}
				k += size(k)
			}
			col = units(brk+1, k)
		}
		p = e
	}

	emit := func(typ int, e int) {
		types = append(types, typ)
		texts = append(texts, src[p:e])
		starts = append(starts, p)
		locs = append(locs, line, col)
		advance(e)
		locs = append(locs, line, col)
	}
	// emitFlat is for a token that cannot hold a line break.
	emitFlat := func(typ int, e int) {
		types = append(types, typ)
		texts = append(texts, src[p:e])
		starts = append(starts, p)
		locs = append(locs, line, col)
		col += units(p, e)
		p = e
		locs = append(locs, line, col)
	}
	// emitChars is for a run that Jison matches one character at a time, where \r\n counts as two line breaks.
	emitChars := func(typ int, e int) {
		types = append(types, typ)
		texts = append(texts, src[p:e])
		starts = append(starts, p)
		locs = append(locs, line, col)
		for k := p; k < e; {
			ch := src[k]
			if ch == 10 || ch == 13 {
				line++
				col = 0
				k++
				continue
			}
			col += units(k, k+size(k))
			k += size(k)
		}
		p = e
		locs = append(locs, line, col)
	}
	emitText := func(typ int, text string, e int) {
		types = append(types, typ)
		texts = append(texts, text)
		starts = append(starts, p)
		locs = append(locs, line, col)
		advance(e)
		locs = append(locs, line, col)
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
	at := func(re *lazyre.Regexp, q int) int {
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

	// wsEnd is the end of the run of whitespace starting at q. The last run is kept, so asking again from inside it costs nothing.
	wsEnd := func(q int) int {
		if q >= wsFrom && q <= wsTo {
			return wsTo
		}
		j := q
		for j < n && isSpace(code(j)) {
			j += size(j)
		}
		wsFrom = q
		wsTo = j
		return j
	}
	blankEnd := func(q int) int {
		if q >= blankFrom && q <= blankTo {
			return blankTo
		}
		j := q
		for j < n {
			ch := src[j]
			if ch != 32 && ch != 9 {
				break
			}
			j++
		}
		blankFrom = q
		blankTo = j
		return j
	}

	// arrowEnd is the end of `\s*--+[x>]\s*` starting at q, or -1.
	arrowEnd := func(q int) int {
		j := wsEnd(q)
		if j == noArrow || code(j) != 45 || code(j+1) != 45 {
			return -1
		}
		k := j + 2
		for code(k) == 45 {
			k++
		}
		t := code(k)
		if t == 120 || t == 62 {
			return wsEnd(k + 1)
		}
		noArrow = j
		return -1
	}

	link := func() bool {
		j := wsEnd(p)
		if j == noLink || code(j) != 45 {
			return false
		}
		d := code(j + 1)
		k := j + 2
		if d == 45 {
			for code(k) == 45 {
				k++
			}
			t := code(k)
			if t == 120 || t == 62 {
				emit(T_LINK, wsEnd(k+1))
			} else {
				push(S_edgeText)
				emit(T_START_LINK, wsEnd(j+2))
			}
			return true
		}
		if d == 46 {
			for code(k) == 46 {
				k++
			}
			if code(k) == 45 {
				emit(T_LINK, wsEnd(k+1))
				return true
			}
		}
		noLink = j
		return false
	}

	// commentEnd: `%%` not followed by `{`, through the end of the line.
	commentEnd := func(q int) int {
		if code(q) != 37 || code(q+1) != 37 || code(q+2) == 123 {
			return -1
		}
		if e := strings.IndexByte(src[q+2:], '\n'); e >= 0 {
			return q + 2 + e
		}
		return n
	}

	quote := func() {
		if code(p+1) == 96 {
			push(S_md_string)
			advance(p + 2)
		} else {
			push(S_string)
			advance(p + 1)
		}
	}

	opener := func(c rune) bool {
		d := code(p + 1)
		if c == 40 {
			switch {
			case d == 45:
				push(S_ellipseText)
				emitFlat(T_ELLIPSE_START, p+2)
			case d == 91:
				push(S_text)
				emitFlat(T_STADIUMSTART, p+2)
			case d == 40 && code(p+2) == 40:
				push(S_text)
				emitFlat(T_DOUBLECIRCLESTART, p+3)
			default:
				push(S_text)
				emitFlat(T_PS, p+1)
			}
			return true
		}
		if c == 91 {
			switch d {
			case 91:
				push(S_text)
				emitFlat(T_SUBROUTINESTART, p+2)
			case 40:
				push(S_text)
				emitFlat(T_CYLINDERSTART, p+2)
			case 47:
				push(S_trapText)
				emitFlat(T_TRAPSTART, p+2)
			case 92:
				push(S_trapText)
				emitFlat(T_INVTRAPSTART, p+2)
			default:
				push(S_text)
				emitFlat(T_SQS, p+1)
			}
			return true
		}
		if c == 124 {
			push(S_text)
			emitFlat(T_PIPE, p+1)
			return true
		}
		if c == 123 {
			push(S_text)
			emitFlat(T_DIAMOND_START, p+1)
			return true
		}
		return false
	}

	nodeString := func() bool {
		q := p
		for q < n {
			ch := src[q]
			if ch < 128 && NODE_CHAR[ch] == 1 {
				q++
			} else if ch == 45 && q+1 < n {
				d := src[q+1]
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
		emitFlat(T_NODE_STRING, q)
		return true
	}

	blanks := func(from int) int {
		e := from
		for e < n {
			ch := src[e]
			if ch != 32 && ch != 9 {
				break
			}
			e++
		}
		return e
	}

	initial := func(c rune) bool {
		var e int
		switch c {
		case 97:
			if strings.HasPrefix(src[p:], "acc") {
				if e = at(RE_ACC_TITLE, p); e >= 0 {
					push(S_acc_title)
					emit(T_acc_title, e)
					return true
				}
				if e = at(RE_ACC_DESCR, p); e >= 0 {
					push(S_acc_descr)
					emit(T_acc_descr, e)
					return true
				}
				if e = at(RE_ACC_DESCR_ML, p); e >= 0 {
					push(S_acc_descr_multiline)
					advance(e)
					return true
				}
			}
			if kw("agentflow-beta") {
				if firstGraph {
					firstGraph = false
					push(S_dir)
				}
				emitFlat(T_GRAPH, p+14)
				return true
			}
		case 64:
			if code(p+1) == 123 {
				push(S_shapeData)
				emitText(T_SHAPE_DATA, "", p+2)
				return true
			}
		case 99:
			if e = at(RE_CALL, p); e >= 0 {
				push(S_callbackname)
				advance(e)
				return true
			}
			if kw("classDef") {
				emitFlat(T_CLASSDEF, p+8)
				return true
			}
			if kw("class") {
				emitFlat(T_CLASS, p+5)
				return true
			}
			if e = at(RE_CLICK, p); e >= 0 {
				push(S_click)
				advance(e)
				return true
			}
			if kw("connector") {
				emitFlat(T_connector, p+9)
				return true
			}
		case 34:
			quote()
			return true
		case 115:
			if kw("style") {
				emitFlat(T_STYLE, p+5)
				return true
			}
		case 100:
			if kw("default") {
				emitFlat(T_DEFAULT, p+7)
				return true
			}
		case 108:
			if kw("linkStyle") {
				emitFlat(T_LINKSTYLE, p+9)
				return true
			}
		case 105:
			if kw("interpolate") {
				emitFlat(T_INTERPOLATE, p+11)
				return true
			}
		case 104:
			if strings.HasPrefix(src[p:], "href") && isSpace(code(p+4)) {
				emit(T_HREF, p+4+size(p+4))
				return true
			}
		case 102:
			if kw("flow") {
				emitFlat(T_flow, p+4)
				return true
			}
		case 103:
			if kw("global") {
				emitFlat(T_global, blanks(p+6))
				return true
			}
		case 101:
			if kw("end") {
				emitFlat(T_end, blanks(p+3))
				return true
			}
		case 95:
			for _, target := range [...]string{"_self", "_blank", "_parent", "_top"} {
				if kw(target) {
					emitFlat(T_LINK_TARGET, p+len(target))
					return true
				}
			}
		}

		if dirNext != -1 && p >= dirFailed {
			if dirNext < p {
				dirNext = strings.Index(src[p:], "direction")
				if dirNext >= 0 {
					dirNext += p
				}
			}
			if dirNext != -1 {
				if lineEnd < p {
					// /[\n\r  ]/g from p.
					lineEnd = n
					for k := p; k < n; k += size(k) {
						if r := code(k); r == 10 || r == 13 || r == 0x2028 || r == 0x2029 {
							lineEnd = k
							break
						}
					}
				}
				if dirNext < lineEnd {
					for i := 0; i < 5; i++ {
						if e = at(RE_DIRECTION[i], p); e >= 0 {
							emit(T_direction_tb+i, e)
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
					atNext = strings.IndexByte(src[p:], '@')
					if atNext >= 0 {
						atNext += p
					}
				}
				q := p
				for q < n {
					ch := code(q)
					if ch == 34 || isSpace(ch) {
						break
					}
					q += size(q)
				}
				runEnd = q
				runAt = -1
				if atNext != -1 && atNext < q {
					for k := min(q, n-1) - 1; k >= atNext; k-- {
						if src[k] != 64 {
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
				emitFlat(T_LINK_ID, runAt+1)
				return true
			}
		}

		if c >= 48 && c <= 57 {
			q := p + 1
			for q < n && src[q] >= 48 && src[q] <= 57 {
				q++
			}
			emitFlat(T_NUM, q)
			return true
		}

		switch c {
		case 35:
			emitFlat(T_BRKT, p+1)
			return true
		case 58:
			if code(p+1) == 58 && code(p+2) == 58 {
				emitFlat(T_STYLE_SEPARATOR, p+3)
			} else {
				emitFlat(T_COLON, p+1)
			}
			return true
		case 38:
			emitFlat(T_AMP, p+1)
			return true
		case 59:
			emitFlat(T_SEMI, p+1)
			return true
		case 44:
			emitFlat(T_COMMA, p+1)
			return true
		case 42:
			emitFlat(T_MULT, p+1)
			return true
		case 60:
			emitFlat(T_TAGSTART, p+1)
			return true
		case 45:
			if link() || nodeString() {
				return true
			}
			emitFlat(T_MINUS, p+1)
			return true
		case 40:
			return opener(c)
		case 91:
			if code(p+1) == 124 {
				emitFlat(T_VERTEX_WITH_PROPS_START, p+2)
				return true
			}
			return opener(c)
		case 124, 123:
			return opener(c)
		case 62:
			push(S_text)
			emitFlat(T_TAGEND, p+1)
			return true
		case 94:
			emitFlat(T_UP, p+1)
			return true
		case 92:
			if code(p+1) == 124 {
				emitFlat(T_SEP, p+2)
				return true
			}
			return nodeString()
		case 118:
			if !util.IsWord(code(p + 1)) {
				emitFlat(T_DOWN, p+1)
				return true
			}
			return nodeString()
		case 37:
			if e = commentEnd(p); e >= 0 {
				emit(T_COMMENT, e)
				return true
			}
			return nodeString()
		}

		if isSpace(c) {
			if link() {
				return true
			}
			if c == 32 || c == 9 {
				if e = commentEnd(blankEnd(p)); e >= 0 {
					emit(T_COMMENT, e)
					return true
				}
			}
			e = p
			for {
				ch := code(e)
				if ch == 10 {
					e++
				} else if ch == 13 && code(e+1) == 10 {
					e += 2
				} else {
					break
				}
			}
			if e > p {
				emit(T_NEWLINE, e)
			} else {
				emit(T_SPACE, p+size(p))
			}
			return true
		}

		if c < 128 {
			return nodeString()
		}
		if util.IsUnicodeLetter(c) {
			emitFlat(T_UNICODE_TEXT, p+size(p))
			return true
		}
		return false
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
				emitFlat(typ, n)
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
					advance(p + 2)
				} else {
					pop()
					advance(p + 1)
				}
			} else {
				e := strings.IndexByte(src[p:], '"')
				if e == -1 {
					e = n
				} else {
					e += p
				}
				emit(T_STR, e)
			}

		case S_md_string:
			switch c {
			case 96:
				if code(p+1) == 34 {
					pop()
					advance(p + 2)
				} else {
					ok = false
				}
			case 34:
				quote()
			default:
				q := p + 1
				for q < n && src[q] != 96 && src[q] != 34 {
					q++
				}
				emit(T_MD_STR, q)
			}

		case S_acc_title, S_acc_descr:
			e := strings.IndexByte(src[p:], '\n')
			if e == -1 {
				e = n
			} else {
				e += p
			}
			typ := T_acc_descr_value
			if state == S_acc_title {
				typ = T_acc_title_value
			}
			pop()
			emit(typ, e)

		case S_acc_descr_multiline:
			if c == 125 {
				pop()
				advance(p + 1)
			} else {
				e := strings.IndexByte(src[p:], '}')
				if e == -1 {
					e = n
				} else {
					e += p
				}
				emit(T_acc_descr_multiline_value, e)
			}

		case S_dir:
			var e int
			if c == 34 {
				quote()
			} else if e = at(RE_NODIR, p); e >= 0 {
				pop()
				emit(T_NODIR, e)
			} else if e = at(RE_DIR, p); e >= 0 {
				pop()
				emit(T_DIR, e)
			} else if e = at(RE_DIR_SYM, p); e >= 0 {
				pop()
				emit(T_DIR, e)
			} else if e = at(RE_DIR_V, p); e >= 0 {
				pop()
				emit(T_DIR, e)
			} else {
				ok = opener(c)
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
					emitFlat(T_STADIUMEND, p+2)
				} else if d == 93 {
					emitFlat(T_SUBROUTINEEND, p+2)
				} else {
					emitFlat(T_SQE, p+1)
				}
			case 41:
				pop()
				if d == 93 {
					emitFlat(T_CYLINDEREND, p+2)
				} else if d == 41 && code(p+2) == 41 {
					emitFlat(T_DOUBLECIRCLEEND, p+3)
				} else {
					emitFlat(T_PE, p+1)
				}
			case 124:
				pop()
				emitFlat(T_PIPE, p+1)
			case 125:
				pop()
				emitFlat(T_DIAMOND_STOP, p+1)
			case 37:
				// Jison drops the lone `%` from Mermaid's text pattern, so only a comment may start with one.
				if e := commentEnd(p); e >= 0 {
					advance(e)
				} else {
					ok = false
				}
			default:
				q := p + 1
				for q < n {
					ch := src[q]
					if ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 124 || ch == 34 || ch == 37 {
						break
					}
					q++
				}
				emit(T_TEXT, q)
			}

		case S_ellipseText:
			d := code(p + 1)
			var e int
			switch {
			case c == 34:
				quote()
			case c == 37 && func() bool { e = commentEnd(p); return e >= 0 }():
				advance(e)
			case (c == 45 || c == 47 || c == 41) && d == 41:
				pop()
				emitFlat(T_ELLIPSE_END, p+2)
			case c == 40 || c == 91 || c == 123:
				opener(c)
			case c == 41 || c == 93 || c == 125:
				ok = false
			default:
				q := p + 1
				for q < n {
					ch := src[q]
					if ch == 34 || ch == 40 || ch == 41 || ch == 91 || ch == 93 || ch == 123 || ch == 125 {
						break
					}
					if (ch == 45 || ch == 47) && code(q+1) == 41 {
						break
					}
					if ch == 37 && code(q+1) == 37 && code(q+2) != 123 {
						break
					}
					q++
				}
				emitChars(T_TEXT, q)
			}

		case S_trapText:
			d := code(p + 1)
			if c == 34 {
				quote()
				break
			}
			switch c {
			case 40:
				if d == 45 || d == 91 || (d == 40 && code(p+2) == 40) {
					opener(c)
				} else if d == 93 {
					pop()
					emitFlat(T_TRAPEND, p+2)
				} else {
					opener(c)
				}
			case 91, 123:
				opener(c)
			case 92:
				if d == 93 {
					pop()
					emitFlat(T_TRAPEND, p+2)
				} else {
					emitFlat(T_TEXT, p+1)
				}
			case 47:
				if d == 93 {
					pop()
					emitFlat(T_INVTRAPEND, p+2)
				} else {
					emitFlat(T_TEXT, p+1)
				}
			case 93, 41:
				if d == 93 {
					pop()
					emitFlat(T_TRAPEND, p+2)
				} else {
					ok = false
				}
			case 125:
				ok = false
			case 37:
				if e := commentEnd(p); e >= 0 {
					advance(e)
				} else {
					ok = false
				}
			default:
				if (c == 63 || c == 61) && d == 93 {
					pop()
					emitFlat(T_TRAPEND, p+2)
					break
				}
				q := p + 1
				for q < n {
					ch := src[q]
					if ch == 92 || ch == 91 || ch == 93 || ch == 40 || ch == 41 || ch == 123 || ch == 125 || ch == 47 || ch == 37 {
						break
					}
					q++
				}
				emit(T_TEXT, q)
			}

		case S_edgeText:
			if c == 34 {
				quote()
				break
			}
			if e := arrowEnd(p); e >= 0 {
				pop()
				emit(T_LINK, e)
				break
			}
			if c == 45 && code(p+1) == 45 {
				ok = false
				break
			}
			q := p + size(p)
			for q < n {
				ch := code(q)
				if ch == 34 {
					break
				}
				if ch == 45 {
					if code(q+1) == 45 {
						break
					}
				} else if isSpace(ch) && arrowEnd(q) >= 0 {
					break
				}
				q += size(q)
			}
			emitChars(T_EDGE_TEXT, q)

		case S_click:
			if c == 34 {
				quote()
			} else if isSpace(c) {
				pop()
				advance(p + size(p))
			} else {
				q := p + size(p)
				for q < n && !isSpace(code(q)) {
					q += size(q)
				}
				emitFlat(T_CLICK, q)
			}

		case S_callbackname:
			if c == 40 {
				e := at(RE_EMPTY_CALL, p)
				pop()
				if e >= 0 {
					advance(e)
				} else {
					push(S_callbackargs)
					advance(p + 1)
				}
			} else {
				e := strings.IndexByte(src[p:], '(')
				if e == -1 {
					e = n
				} else {
					e += p
				}
				emit(T_CALLBACKNAME, e)
			}

		case S_callbackargs:
			if c == 41 {
				pop()
				advance(p + 1)
			} else {
				e := strings.IndexByte(src[p:], ')')
				if e == -1 {
					e = n
				} else {
					e += p
				}
				emit(T_CALLBACKARGS, e)
			}

		case S_shapeData:
			switch c {
			case 34:
				push(S_shapeDataStr)
				emitFlat(T_SHAPE_DATA, p+1)
			case 125:
				pop()
				advance(p + 1)
			case 94:
				ok = false
			default:
				q := p + 1
				for q < n && src[q] != 125 && src[q] != 94 && src[q] != 34 {
					q++
				}
				emit(T_SHAPE_DATA, q)
			}

		case S_shapeDataStr:
			if c == 34 {
				pop()
				emitFlat(T_SHAPE_DATA, p+1)
			} else {
				e := strings.IndexByte(src[p:], '"')
				if e == -1 {
					e = n
				} else {
					e += p
				}
				emitText(T_SHAPE_DATA, RE_BR.ReplaceAllString(src[p:e], "<br/>"), e)
			}
		}

		if !ok {
			types = append(types, T_ERROR)
			texts = append(texts, "")
			starts = append(starts, p)
			locs = append(locs, line, col, line, col)
			return Tokens{types, texts, starts, locs}
		}
	}

	types = append(types, T_END)
	texts = append(texts, "")
	starts = append(starts, n)
	locs = append(locs, line, col, line, col)
	return Tokens{types, texts, starts, locs}
}
