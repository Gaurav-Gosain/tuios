// Ported from pele src/diagrams/common/outline.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The port reads UTF-8 bytes: offsets are byte offsets, and a character that may not be
// ASCII is decoded where a pattern tells it apart. The tokenizer's closures are methods of
// a struct, and the sticky regular expressions are hand matchers.

package common

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

// The indented outline grammar that Mermaid's mindmap and kanban diagrams share.
// Kanban adds `@{ ... }` metadata after a node and keeps `@` out of node ids.

const (
	T_END = iota
	T_ERROR
	T_SPACELINE
	T_NL
	T_EOF
	T_KEYWORD
	T_SPACELIST
	T_ICON
	T_CLASS
	T_NODE_DSTART
	T_NODE_ID
	T_NODE_DESCR
	T_NODE_DEND
	T_SHAPE_DATA
)

// As the generated parser prints them. It has no name for the end of input and prints its number.
var NAMES = [...]string{
	"1",
	"INVALID",
	"SPACELINE",
	"NL",
	"EOF",
	"",
	"SPACELIST",
	"ICON",
	"CLASS",
	"NODE_DSTART",
	"NODE_ID",
	"NODE_DESCR",
	"NODE_DEND",
	"SHAPE_DATA",
}

func TokenName(typ int, kanban bool) string {
	if typ == T_KEYWORD {
		if kanban {
			return "KANBAN"
		}
		return "MINDMAP"
	}
	return NAMES[typ]
}

type OutlineTokens struct {
	Types  []int
	Texts  []string
	Starts []int
}

const (
	NODE_TYPE_DEFAULT      = 0
	NODE_TYPE_NO_BORDER    = 0
	NODE_TYPE_ROUNDED_RECT = 1
	NODE_TYPE_RECT         = 2
	NODE_TYPE_CIRCLE       = 3
	NODE_TYPE_CLOUD        = 4
	NODE_TYPE_BANG         = 5
	NODE_TYPE_HEXAGON      = 6
)

// GetType: any opening delimiter pairs with any closing one, as in Mermaid.
func GetType(start, end string) int {
	switch start {
	case "[":
		return NODE_TYPE_RECT
	case "(":
		if end == ")" {
			return NODE_TYPE_ROUNDED_RECT
		}
		return NODE_TYPE_CLOUD
	case "((":
		return NODE_TYPE_CIRCLE
	case ")":
		return NODE_TYPE_CLOUD
	case "))":
		return NODE_TYPE_BANG
	case "{{":
		return NODE_TYPE_HEXAGON
	}
	return NODE_TYPE_DEFAULT
}

// Decoration is an icon or a class for the node before it. An empty field is none.
type Decoration struct {
	Icon  string
	Class string
}

type OutlineDb interface {
	GetType(start, end string) int
	// shapeData is nil when the node has none.
	AddNode(level int, id, descr string, typ int, shapeData *string)
	DecorateNode(decoration Decoration)
}

const (
	S_INITIAL = iota
	S_CLASS
	S_ICON
	S_NODE
	S_NSTR
	S_NSTR2
	S_DATA
	S_DATA_STR
)

// The character at a byte offset, as charCodeAt reads it: -1 past the end.
func codeAt(src string, q int) rune {
	if q < 0 || q >= len(src) {
		return -1
	}
	if src[q] < utf8.RuneSelf {
		return rune(src[q])
	}
	r, _ := utf8.DecodeRuneInString(src[q:])
	return r
}

// Jison's case-insensitive flag folds ASCII letters only.
//
//go:noinline
func letters(src string, p int, word string) bool {
	for k := 0; k < len(word); k++ {
		if (codeAt(src, p+k) | 32) != rune(word[k]) {
			return false
		}
	}
	return true
}

type outlineLexer struct {
	src       string
	n         int
	p         int
	state     int
	types     []int
	texts     []string
	starts    []int
	nextBreak int
}

//go:noinline
func (l *outlineLexer) put(typ int, text string, end int) {
	l.types = append(l.types, typ)
	l.texts = append(l.texts, text)
	l.starts = append(l.starts, l.p)
	l.p = end
}

//go:noinline
func (l *outlineLexer) emit(typ, end, next int) {
	l.put(typ, l.src[l.p:end], end)
	l.state = next
}

//go:noinline
func (l *outlineLexer) upTo(ch byte) int {
	e := strings.IndexByte(l.src[l.p:], ch)
	if e == -1 {
		return l.n
	}
	return l.p + e
}

// /\s+/y
func reSpace(src string, from int) int {
	q := from
	for q < len(src) {
		c, size := utf8.DecodeRuneInString(src[q:])
		if !js.IsSpace(c) {
			break
		}
		q += size
	}
	if q == from {
		return -1
	}
	return q
}

// /.+/y
func reLine(src string, from int) int {
	q := from
	for {
		c := codeAt(src, q)
		if c < 0 || c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029 {
			break
		}
		if c < utf8.RuneSelf {
			q++
		} else {
			_, size := utf8.DecodeRuneInString(src[q:])
			q += size
		}
	}
	if q == from {
		return -1
	}
	return q
}

// A sticky match of a run of bytes outside `stop`, which holds ASCII characters only.
//
//go:noinline
func reNot(src string, from int, stop string) int {
	q := from
	for q < len(src) && strings.IndexByte(stop, src[q]) < 0 {
		q++
	}
	if q == from {
		return -1
	}
	return q
}

const (
	RE_ID        = "([\n){}"
	RE_ID_KANBAN = "([\n){}@"
	RE_DESCR     = ")](}"
	RE_MD        = "`\""
	RE_DATA      = "}^\""
)

// .replace(/\n\s*/g, '<br/>')
func replaceBreaks(text string) string {
	if strings.IndexByte(text, '\n') < 0 {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		out.WriteString("<br/>")
		i++
		if q := reSpace(text, i); q > 0 {
			i = q
		}
	}
	return out.String()
}

func TokenizeOutline(src string, kanban bool) OutlineTokens {
	l := &outlineLexer{src: src, n: len(src), state: S_INITIAL, nextBreak: -1}
	n := l.n
	keyword := "mindmap"
	reId := RE_ID
	if kanban {
		keyword = "kanban"
		reId = RE_ID_KANBAN
	}

	for l.p < n {
		p := l.p
		c := codeAt(src, p)
		// d is compared only when c is ASCII, where the next byte starts the next character.
		d := codeAt(src, p+1)
		var e int
		switch l.state {
		case S_INITIAL:
			w := p
			if c <= 32 || c >= 160 {
				w = max(reSpace(src, p), p)
			}
			if kanban && c == 64 && d == 123 {
				l.put(T_SHAPE_DATA, "", p+2)
				l.state = S_DATA
			} else if codeAt(src, w) == 37 && codeAt(src, w+1) == 37 {
				l.emit(T_SPACELINE, reLine(src, w), S_INITIAL)
			} else if w > p {
				if l.nextBreak < p {
					l.nextBreak = l.upTo('\n')
				}
				if l.nextBreak >= w {
					l.emit(T_SPACELIST, w, S_INITIAL)
				} else if e = strings.LastIndexByte(src[:w], '\n'); e > p {
					l.emit(T_SPACELINE, e+1, S_INITIAL)
				} else {
					l.emit(T_NL, p+1, S_INITIAL)
				}
			} else if letters(src, p, keyword) && !util.IsWord(codeAt(src, p+len(keyword))) {
				l.emit(T_KEYWORD, p+len(keyword), S_INITIAL)
			} else if c == 58 && d == 58 && codeAt(src, p+2) == 58 {
				l.p += 3
				l.state = S_CLASS
			} else if c == 58 && d == 58 && letters(src, p+2, "icon") && codeAt(src, p+6) == 40 {
				l.p += 7
				l.state = S_ICON
			} else if (c == 45 && d == 41) || (c == 40 && (d == 45 || d == 40)) || ((c == 41 || c == 123) && d == c) {
				l.emit(T_NODE_DSTART, p+2, S_NODE)
			} else if c == 41 || c == 40 || c == 91 {
				l.emit(T_NODE_DSTART, p+1, S_NODE)
			} else if e = reNot(src, p, reId); e != -1 {
				l.emit(T_NODE_ID, e, S_INITIAL)
			} else {
				goto done
			}
		case S_NODE:
			if c == 34 {
				if d == 96 {
					l.state = S_NSTR2
					l.p += 2
				} else {
					l.state = S_NSTR
					l.p++
				}
			} else if c == 41 || c == 40 {
				end := p + 1
				if d == c || (c == 40 && d == 45) {
					end = p + 2
				}
				l.emit(T_NODE_DEND, end, S_INITIAL)
			} else if c == 93 {
				l.emit(T_NODE_DEND, p+1, S_INITIAL)
			} else if (c == 125 && d == 125) || (c == 45 && d == 41) {
				l.emit(T_NODE_DEND, p+2, S_INITIAL)
			} else if c == 125 {
				// A lone closing brace takes the rest of its line as text.
				l.emit(T_NODE_DESCR, reLine(src, p), S_NODE)
			} else {
				l.emit(T_NODE_DESCR, reNot(src, p, RE_DESCR), S_NODE)
			}
		case S_NSTR:
			if c == 34 {
				l.p++
				l.state = S_NODE
			} else {
				l.emit(T_NODE_DESCR, l.upTo('"'), S_NSTR)
			}
		case S_NSTR2:
			if c == 96 && d == 34 {
				l.p += 2
				l.state = S_NODE
			} else if e = reNot(src, p, RE_MD); e != -1 {
				l.emit(T_NODE_DESCR, e, S_NSTR2)
			} else {
				goto done
			}
		case S_CLASS:
			if e = reLine(src, p); e != -1 {
				l.emit(T_CLASS, e, S_INITIAL)
			} else if c == 10 {
				l.p++
				l.state = S_INITIAL
			} else {
				goto done
			}
		case S_ICON:
			if c == 41 {
				l.p++
				l.state = S_INITIAL
			} else {
				l.emit(T_ICON, l.upTo(')'), S_ICON)
			}
		case S_DATA:
			if c == 34 {
				l.emit(T_SHAPE_DATA, p+1, S_DATA_STR)
			} else if c == 125 {
				l.p++
				l.state = S_INITIAL
			} else if e = reNot(src, p, RE_DATA); e != -1 {
				l.emit(T_SHAPE_DATA, e, S_DATA)
			} else {
				goto done
			}
		default:
			if c == 34 {
				l.emit(T_SHAPE_DATA, p+1, S_DATA)
			} else {
				e = l.upTo('"')
				l.put(T_SHAPE_DATA, replaceBreaks(src[p:e]), e)
			}
		}
	}
done:
	if l.p < n {
		l.put(T_ERROR, "", l.p)
	} else {
		if l.state == S_INITIAL {
			l.put(T_EOF, "", n)
		}
		l.put(T_END, "", n)
	}
	return OutlineTokens{l.types, l.texts, l.starts}
}

const STOP = "'SPACELINE', 'NL', 'EOF'"

var START = func() (start [T_SHAPE_DATA + 1]uint8) {
	for _, t := range []int{T_SPACELINE, T_SPACELIST, T_ICON, T_CLASS, T_NODE_DSTART, T_NODE_ID} {
		start[t] = 1
	}
	return
}()

func isStop(typ int) bool {
	return typ == T_SPACELINE || typ == T_NL || typ == T_EOF
}

type outlineParser struct {
	src     string
	diagram string
	kanban  bool
	tokens  OutlineTokens
	i       int
}

// The type of token i. Past the end, which the parser never reads, it is the end of input.
func (o *outlineParser) typ(i int) int {
	if i >= len(o.tokens.Types) {
		return T_END
	}
	return o.tokens.Types[i]
}

func (o *outlineParser) text(i int) string {
	if i >= len(o.tokens.Texts) {
		return ""
	}
	return o.tokens.Texts[i]
}

func (o *outlineParser) fail(expected string) {
	i := min(o.i, len(o.tokens.Starts)-1)
	t := o.typ(o.i)
	if t == T_ERROR {
		panic(types.SyntaxError(o.diagram, o.src, o.tokens.Starts[i], "", true))
	}
	panic(types.SyntaxError(o.diagram, o.src, o.tokens.Starts[i], "Expecting "+expected+", got '"+TokenName(t, o.kanban)+"'", false))
}

// ParseOutline makes the calls on `db` that the grammar's actions make, in the same order, and reports
// errors in the state where the generated parser would.
func ParseOutline(src string, db OutlineDb, kanban bool) {
	diagram := "mindmap"
	data := ""
	if kanban {
		diagram = "kanban"
		data = ", 'SHAPE_DATA'"
	}
	o := &outlineParser{src: src, diagram: diagram, kanban: kanban, tokens: TokenizeOutline(src, kanban)}

	keyword := "'" + TokenName(T_KEYWORD, kanban) + "'"
	if o.typ(o.i) == T_SPACELINE {
		o.i++
		for o.typ(o.i) == T_SPACELINE || o.typ(o.i) == T_NL {
			o.i++
		}
		if o.typ(o.i) != T_KEYWORD {
			o.fail("'SPACELINE', 'NL', " + keyword)
		}
	} else if o.typ(o.i) != T_KEYWORD {
		o.fail("'SPACELINE', " + keyword)
	}
	o.i++
	first := "'SPACELINE', 'NL', 'SPACELIST', 'ICON', 'CLASS', 'NODE_DSTART', 'NODE_ID'"
	if o.typ(o.i) == T_NL {
		o.i++
		first = "'SPACELINE', 'SPACELIST', 'ICON', 'CLASS', 'NODE_DSTART', 'NODE_ID'"
	}

	for {
		t := o.typ(o.i)
		level := 0
		indented := t == T_SPACELIST
		if indented {
			level = js.Len(o.text(o.i))
			o.i++
			t = o.typ(o.i)
		}
		if t == T_NODE_ID || t == T_NODE_DSTART {
			var id *string
			var descr string
			typ := 0
			if t == T_NODE_ID {
				text := o.text(o.i)
				id = &text
				o.i++
			}
			if o.typ(o.i) == T_NODE_DSTART {
				start := o.text(o.i)
				o.i++
				if o.typ(o.i) != T_NODE_DESCR {
					o.fail("'NODE_DESCR'")
				}
				descr = o.text(o.i)
				o.i++
				if o.typ(o.i) != T_NODE_DEND {
					o.fail("'NODE_DEND'")
				}
				end := o.text(o.i)
				o.i++
				if !isStop(o.typ(o.i)) && o.typ(o.i) != T_SHAPE_DATA {
					o.fail(STOP + data)
				}
				typ = db.GetType(start, end)
			} else {
				if !isStop(o.typ(o.i)) && o.typ(o.i) != T_SHAPE_DATA {
					o.fail(STOP + ", 'NODE_DSTART'" + data)
				}
				descr = *id
			}
			nodeId := descr
			if id != nil {
				nodeId = *id
			}
			if o.typ(o.i) == T_SHAPE_DATA {
				// One Builder, not a += per token: Go copies the string on each +=, where JavaScript ropes do not.
				var shape strings.Builder
				shape.WriteString(o.text(o.i))
				o.i++
				for o.typ(o.i) == T_SHAPE_DATA {
					shape.WriteString(o.text(o.i))
					o.i++
				}
				shapeData := shape.String()
				if !isStop(o.typ(o.i)) {
					o.fail(STOP + data)
				}
				db.AddNode(level, nodeId, descr, typ, &shapeData)
			} else {
				db.AddNode(level, nodeId, descr, typ, nil)
			}
		} else if t == T_ICON || t == T_CLASS {
			text := o.text(o.i)
			o.i++
			if !isStop(o.typ(o.i)) {
				o.fail(STOP)
			}
			if t == T_ICON {
				db.DecorateNode(Decoration{Icon: text})
			} else {
				db.DecorateNode(Decoration{Class: text})
			}
		} else if indented {
			if !isStop(t) {
				o.fail(STOP + ", 'ICON', 'CLASS', 'NODE_DSTART', 'NODE_ID'")
			}
		} else if t == T_SPACELINE {
			o.i++
			if !isStop(o.typ(o.i)) {
				o.fail(STOP)
			}
		} else {
			o.fail(first)
		}

		o.i++
		for o.typ(o.i) == T_NL || o.typ(o.i) == T_EOF {
			o.i++
		}
		if o.typ(o.i) == T_END {
			return
		}
		if START[o.typ(o.i)] == 0 {
			o.fail(STOP + ", 'SPACELIST', 'ICON', 'CLASS', 'NODE_DSTART', 'NODE_ID'")
		}
	}
}
