// Ported from pele src/diagrams/er/parser.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package er

import (
	"slices"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// cardinality is CARDINALITY.get(t): "" for a token that is no cardinality.
func cardinality(t int) Cardinality {
	switch t {
	case T_ZERO_OR_ONE:
		return "ZERO_OR_ONE"
	case T_ZERO_OR_MORE:
		return "ZERO_OR_MORE"
	case T_ONE_OR_MORE:
		return "ONE_OR_MORE"
	case T_ONLY_ONE:
		return "ONLY_ONE"
	case T_MD_PARENT:
		return "MD_PARENT"
	}
	return ""
}

var DIRECTIONS = [4]string{"TB", "BT", "RL", "LR"}

const NAME = "'UNICODE_TEXT', 'NUM', 'ENTITY_NAME', 'DECIMAL_NUM', 'ENTITY_ONE'"
const CARDS = "'ZERO_OR_ONE', 'ZERO_OR_MORE', 'ONE_OR_MORE', 'ONLY_ONE', 'MD_PARENT'"
const LINE = "'EOF', 'NEWLINE', 'title', 'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'SUBGRAPH', 'direction_tb', " +
	"'direction_bt', 'direction_rl', 'direction_lr', 'CLASSDEF', 'CLASS', 'STYLE', " +
	NAME

type Frame struct {
	id    string
	title string
	// What Mermaid's grammar hands to addSubGraph: the value of every statement inside. Besides entity
	// names that includes accessibility text and the keywords `style`, `class` and `classDef`.
	list []DocItem
}

type parser struct {
	src    string
	db     *ErDb
	types  []int
	texts  []string
	starts []int
	i      int
}

func (ps *parser) fail(expected string) {
	t := ps.types[ps.i]
	if t == T_ERROR {
		panic(types.SyntaxError("er", ps.src, ps.starts[ps.i], "", true))
	}
	got := TOKEN_NAMES[t]
	if t == T_CHAR {
		got = ps.texts[ps.i]
	}
	panic(types.SyntaxError("er", ps.src, ps.starts[ps.i], "Expecting "+expected+", got '"+got+"'", false))
}

//go:noinline
func (ps *parser) expect(typ int) string {
	if ps.types[ps.i] != typ {
		ps.fail("'" + TOKEN_NAMES[typ] + "'")
	}
	ps.i++
	return ps.texts[ps.i-1]
}

func (ps *parser) separator() {
	t := ps.types[ps.i]
	if t != T_NEWLINE && t != T_SEMI && t != T_EOF {
		ps.fail("'EOF', 'NEWLINE', 'SEMI'")
	}
	ps.i++
}

func isName(t int) bool {
	return t == T_UNICODE_TEXT || t == T_ENTITY_NAME || t == T_NUM || t == T_DECIMAL_NUM || t == T_ENTITY_ONE
}

// unquote is text.slice(1, -1) for a token that opens and closes with a quote.
func unquote(text string) string {
	if len(text) < 2 {
		return ""
	}
	return text[1 : len(text)-1]
}

func (ps *parser) entityName() string {
	t := ps.types[ps.i]
	if !isName(t) {
		ps.fail(NAME)
	}
	ps.i++
	text := ps.texts[ps.i-1]
	if t == T_ENTITY_NAME {
		return unquote(text)
	}
	return text
}

func (ps *parser) idList() []string {
	var ids []string
	for {
		t := ps.types[ps.i]
		if t != T_UNICODE_TEXT && t != T_STYLE_TEXT {
			ps.fail("'UNICODE_TEXT', 'STYLE_TEXT'")
		}
		ids = append(ids, ps.texts[ps.i])
		ps.i++
		if ps.types[ps.i] != T_COMMA {
			return ids
		}
		ps.i++
	}
}

func isStyleComponent(t int) bool {
	return t == T_STYLE_TEXT || t == T_NUM || t == T_COLON || t == T_BRKT
}

func (ps *parser) stylesOpt() []string {
	var styles []string
	for {
		if !isStyleComponent(ps.types[ps.i]) {
			ps.fail("'STYLE_TEXT', 'COLON', 'NUM', 'BRKT'")
		}
		var style strings.Builder
		style.WriteString(ps.texts[ps.i])
		ps.i++
		for isStyleComponent(ps.types[ps.i]) {
			style.WriteString(ps.texts[ps.i])
			ps.i++
		}
		styles = append(styles, style.String())
		if ps.types[ps.i] != T_COMMA {
			return styles
		}
		ps.i++
	}
}

func (ps *parser) relSpec() RelSpec {
	cardB := cardinality(ps.types[ps.i])
	ps.i++
	r := ps.types[ps.i]
	if r != T_IDENTIFYING && r != T_NON_IDENTIFYING {
		ps.fail("'NON_IDENTIFYING', 'IDENTIFYING'")
	}
	relType := "NON_IDENTIFYING"
	if r == T_IDENTIFYING {
		relType = "IDENTIFYING"
	}
	ps.i++
	cardA := cardinality(ps.types[ps.i])
	if cardA == "" {
		ps.fail(CARDS)
	}
	ps.i++
	return RelSpec{cardA: cardA, relType: relType, cardB: cardB}
}

func (ps *parser) role() string {
	t := ps.types[ps.i]
	if t != T_WORD && t != T_ENTITY_NAME && t != T_UNICODE_TEXT {
		ps.fail("'UNICODE_TEXT', 'ENTITY_NAME', 'WORD'")
	}
	ps.i++
	text := ps.texts[ps.i-1]
	if t == T_UNICODE_TEXT {
		return text
	}
	return unquote(text)
}

func (ps *parser) isChar(ch string) bool {
	return ps.types[ps.i] == T_CHAR && ps.texts[ps.i] == ch
}

// Returns the attributes last to first, as Mermaid's right-recursive rule builds them.
func (ps *parser) attributes() []*ParsedAttribute {
	var list []*ParsedAttribute
	for {
		typ := ps.expect(T_ATTRIBUTE_WORD)
		if ps.isChar("?") {
			typ += ps.texts[ps.i]
			ps.i++
		}
		if ps.types[ps.i] != T_ATTRIBUTE_WORD {
			if strings.HasSuffix(typ, "?") {
				ps.fail("'ATTRIBUTE_WORD'")
			}
			ps.fail("'ATTRIBUTE_WORD', '?'")
		}
		attribute := &ParsedAttribute{typ: typ, name: ps.texts[ps.i]}
		ps.i++
		if ps.types[ps.i] == T_ATTRIBUTE_KEY {
			keys := []string{ps.texts[ps.i]}
			ps.i++
			for ps.isChar(",") {
				ps.i++
				keys = append(keys, ps.expect(T_ATTRIBUTE_KEY))
			}
			attribute.keys = keys
		}
		if ps.types[ps.i] == T_COMMENT {
			attribute.comment = unquote(ps.texts[ps.i])
			ps.i++
		}
		list = append(list, attribute)
		if ps.types[ps.i] == T_BLOCK_STOP {
			break
		}
	}
	slices.Reverse(list)
	return list
}

// Parses an optional `{ ... }` block; nil when there is none or it is empty.
func (ps *parser) block() []*ParsedAttribute {
	if ps.types[ps.i] != T_BLOCK_START {
		return nil
	}
	ps.i++
	if ps.types[ps.i] == T_BLOCK_STOP {
		ps.i++
		return nil
	}
	list := ps.attributes()
	ps.i++
	return list
}

func (ps *parser) entityStatement() []string {
	db := ps.db
	a := ps.entityName()
	t := ps.types[ps.i]
	if t == T_SQS {
		ps.i++
		alias := ps.entityName()
		ps.expect(T_SQE)
		var classes []string
		if ps.types[ps.i] == T_STYLE_SEPARATOR {
			ps.i++
			classes = ps.idList()
		}
		attrs := ps.block()
		db.addEntity(a, alias)
		if attrs != nil {
			db.addAttributes(a, attrs)
		}
		if classes != nil {
			db.setClass([]string{a}, classes)
		}
		return []string{a}
	}
	var classes []string
	if t == T_STYLE_SEPARATOR {
		ps.i++
		classes = ps.idList()
		t = ps.types[ps.i]
	}
	if cardinality(t) != "" {
		spec := ps.relSpec()
		b := ps.entityName()
		var classesB []string
		if ps.types[ps.i] == T_STYLE_SEPARATOR {
			ps.i++
			classesB = ps.idList()
		}
		ps.expect(T_COLON)
		label := ps.role()
		db.addEntity(a, "")
		db.addEntity(b, "")
		db.addRelationship(a, label, b, spec)
		if classes != nil {
			db.setClass([]string{a}, classes)
		}
		if classesB != nil {
			db.setClass([]string{b}, classesB)
		}
		return []string{a, b}
	}
	attrs := ps.block()
	db.addEntity(a, "")
	if attrs != nil {
		db.addAttributes(a, attrs)
	}
	if classes != nil {
		db.setClass([]string{a}, classes)
	}
	return []string{a}
}

func parseEr(src string, db *ErDb) {
	toks := tokenize(src)
	// The token arrays end in END, or in ERROR, which no rule takes; the padding keeps
	// every look past the last token in bounds.
	for k := 0; k < 4; k++ {
		toks.types = append(toks.types, T_END)
		toks.texts = append(toks.texts, "")
		toks.starts = append(toks.starts, len(src))
	}
	ps := &parser{src: src, db: db, types: toks.types, texts: toks.texts, starts: toks.starts}
	ps.run()
}

func (ps *parser) run() {
	db := ps.db
	ps.expect(T_ER_DIAGRAM)
	// Subgraphs nest on this stack rather than on the call stack; the first frame is the diagram itself.
	frames := []*Frame{{}}
	frame := frames[0]
	nested := false

	for {
		t := ps.types[ps.i]
		switch t {
		case T_NEWLINE:
			ps.i++
		case T_EOF:
			ps.i++
			if nested {
				ps.fail("'END', " + LINE)
			}
			return
		case T_END_KW:
			if !nested {
				ps.fail(LINE)
			}
			ps.i++
			id := db.addSubGraph(frame.id, frame.list, frame.title)
			frames = frames[:len(frames)-1]
			frame = frames[len(frames)-1]
			nested = len(frames) > 1
			if nested {
				frame.list = append(frame.list, DocItem{value: id})
			}
		case T_SUBGRAPH:
			ps.i++
			id := ps.entityName()
			title := id
			if ps.types[ps.i] == T_SQS {
				ps.i++
				title = ps.entityName()
				for isName(ps.types[ps.i]) {
					title += " " + ps.entityName()
				}
				ps.expect(T_SQE)
			}
			ps.separator()
			frame = &Frame{id: id, title: title}
			frames = append(frames, frame)
			nested = true
		case T_acc_title:
			ps.i++
			value := js.Trim(ps.expect(T_acc_title_value))
			db.setAccTitle(value)
			if nested {
				frame.list = append(frame.list, DocItem{value: value})
			}
		case T_acc_descr:
			ps.i++
			value := js.Trim(ps.expect(T_acc_descr_value))
			db.setAccDescription(value)
			if nested {
				frame.list = append(frame.list, DocItem{value: value})
			}
		case T_acc_descr_multiline_value:
			value := js.Trim(ps.texts[ps.i])
			ps.i++
			db.setAccDescription(value)
			if nested {
				frame.list = append(frame.list, DocItem{value: value})
			}
		case T_direction_tb, T_direction_bt, T_direction_rl, T_direction_lr:
			ps.i++
			value := DIRECTIONS[t-T_direction_tb]
			if nested {
				frame.list = append(frame.list, DocItem{dir: true, value: value})
			} else {
				db.setDirection(value)
			}
		case T_CLASSDEF:
			keyword := ps.texts[ps.i]
			ps.i++
			ids := ps.idList()
			styles := ps.stylesOpt()
			ps.separator()
			db.addClass(ids, styles)
			if nested {
				frame.list = append(frame.list, DocItem{value: keyword})
			}
		case T_CLASS:
			keyword := ps.texts[ps.i]
			ps.i++
			ids := ps.idList()
			db.setClass(ids, ps.idList())
			if nested {
				frame.list = append(frame.list, DocItem{value: keyword})
			}
		case T_STYLE:
			keyword := ps.texts[ps.i]
			ps.i++
			ids := ps.idList()
			styles := ps.stylesOpt()
			ps.separator()
			db.addCssStyles(ids, styles)
			if nested {
				frame.list = append(frame.list, DocItem{value: keyword})
			}
		default:
			if !isName(t) {
				if nested {
					ps.fail("'END', " + LINE)
				}
				ps.fail(LINE)
			}
			names := ps.entityStatement()
			if nested {
				for _, name := range names {
					frame.list = append(frame.list, DocItem{value: name})
				}
			}
		}
	}
}
