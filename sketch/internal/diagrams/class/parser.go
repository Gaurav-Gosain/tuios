// Ported from pele src/diagrams/class/parser.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package class

import (
	"slices"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const COUNT = T_PUNCTUATION + 1

var ALPHANUM = func() (out [COUNT]uint8) {
	for _, t := range [...]int{T_UNICODE_TEXT, T_ALPHA, T_MINUS} {
		out[t] = 1
	}
	return
}()

var STYLE_PART = func() (out [COUNT]uint8) {
	for _, t := range [...]int{T_ALPHA, T_COLON, T_BRKT, T_STYLE, T_PCT, T_LABEL} {
		out[t] = 1
	}
	return
}()

// Mermaid's relationType numbers, plus one so that zero means "not an end marker".
var END_TYPE = func() (out [COUNT]uint8) {
	out[T_AGGREGATION] = 1
	out[T_EXTENSION] = 2
	out[T_COMPOSITION] = 3
	out[T_DEPENDENCY] = 4
	out[T_LOLLIPOP] = 5
	return
}()

const NAME_TOKENS = "'UNICODE_TEXT', 'NUM', 'ALPHA', 'MINUS', 'BQUOTE_STR'"

type Frame struct {
	id      string
	classes []string
	notes   []string
}

type parser struct {
	src    string
	db     *ClassDb
	types  []int
	texts  []string
	starts []int
	i      int
}

func (ps *parser) fail(expected string) {
	t := ps.types[ps.i]
	if t == T_ERROR {
		panic(types.SyntaxError("class", ps.src, ps.starts[ps.i], "", true))
	}
	panic(types.SyntaxError("class", ps.src, ps.starts[ps.i], "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
}

func (ps *parser) expect(typ int) string {
	if ps.types[ps.i] != typ {
		ps.fail("'" + TOKEN_NAMES[typ] + "'")
	}
	ps.i++
	return ps.texts[ps.i-1]
}

// text takes the current token's text.
func (ps *parser) text() string {
	ps.i++
	return ps.texts[ps.i-1]
}

func (ps *parser) alphaNum() string {
	if ALPHANUM[ps.types[ps.i]] == 0 {
		ps.fail("'UNICODE_TEXT', 'NUM', 'ALPHA', 'MINUS'")
	}
	return ps.text()
}

func (ps *parser) name(generic bool) string {
	out := ""
	for {
		if ps.types[ps.i] == T_BQUOTE_STR {
			out += ps.text()
			break
		}
		if ALPHANUM[ps.types[ps.i]] == 0 {
			ps.fail(NAME_TOKENS)
		}
		out += ps.text()
		t := ps.types[ps.i]
		if t == T_DOT {
			out += "."
			ps.i++
		} else if ALPHANUM[t] == 0 && t != T_BQUOTE_STR {
			break
		}
	}
	if generic && ps.types[ps.i] == T_GENERICTYPE {
		out += "~" + ps.text() + "~"
	}
	return out
}

func (ps *parser) classLabel() string {
	ps.i++
	label := ps.expect(T_STR)
	ps.expect(T_SQE)
	return label
}

func (ps *parser) members() []string {
	if ps.types[ps.i] != T_MEMBER {
		ps.fail("'STRUCT_STOP', 'MEMBER'")
	}
	var list []string
	for ps.types[ps.i] == T_MEMBER {
		list = append(list, ps.text())
	}
	ps.expect(T_STRUCT_STOP)
	// The grammar collects members last to first, and addMembers turns them back.
	slices.Reverse(list)
	return list
}

func (ps *parser) classStatement() string {
	db := ps.db
	ps.i++
	id := ps.name(true)
	db.addClass(id)
	if ps.types[ps.i] == T_SQS {
		db.setClassLabel(id, ps.classLabel())
	}
	switch ps.types[ps.i] {
	case T_STYLE_SEPARATOR:
		ps.i++
		css := ps.alphaNum()
		db.setCssClass(id, css)
		if ps.types[ps.i] == T_STRUCT_START {
			ps.i++
			db.addMembers(id, ps.members())
		}
	case T_STRUCT_START:
		ps.i++
		if ps.types[ps.i] == T_STRUCT_STOP {
			ps.i++
		} else {
			db.addMembers(id, ps.members())
		}
	case T_ANNOTATION_START:
		ps.i++
		annotation := ps.alphaNum()
		ps.expect(T_ANNOTATION_END)
		db.addAnnotation(id, annotation)
		if ps.types[ps.i] == T_STRUCT_START {
			ps.i++
			if ps.types[ps.i] == T_STRUCT_STOP {
				ps.i++
			} else {
				db.addMembers(id, ps.members())
			}
		}
	}
	return id
}

func (ps *parser) noteStatement() string {
	ps.i++
	if ps.types[ps.i-1] == T_NOTE {
		return ps.db.addNote(ps.expect(T_STR), nil)
	}
	id := ps.name(true)
	return ps.db.addNote(ps.expect(T_STR), &id)
}

// Namespaces nest, so they are parsed with a stack of open ones.
func (ps *parser) namespaceStatement() {
	db := ps.db
	var open []*Frame
	for {
		ps.i++
		ns := ps.name(false)
		var id string
		if ps.types[ps.i] == T_SQS {
			id = db.addNamespace(ns, ps.classLabel())
		} else {
			id = db.addNamespace(ns, "")
		}
		ps.expect(T_STRUCT_START)
		if ps.types[ps.i] == T_NEWLINE {
			ps.i++
		}
		open = append(open, &Frame{id: id})
		item := true
		for item {
			frame := open[len(open)-1]
			t := ps.types[ps.i]
			if t == T_NAMESPACE {
				break
			}
			if t == T_CLASS {
				frame.classes = append(frame.classes, ps.classStatement())
			} else if t == T_NOTE || t == T_NOTE_FOR {
				frame.notes = append(frame.notes, ps.noteStatement())
			} else {
				ps.fail("'NAMESPACE', 'CLASS', 'NOTE_FOR', 'NOTE'")
			}
			for {
				if ps.types[ps.i] == T_NEWLINE {
					next := ps.types[ps.i+1]
					if next == T_NAMESPACE || next == T_CLASS || next == T_NOTE || next == T_NOTE_FOR {
						ps.i++
						break
					}
					ps.i++
				}
				ps.expect(T_STRUCT_STOP)
				done := open[len(open)-1]
				open = open[:len(open)-1]
				db.addClassesToNamespace(done.id, done.classes, done.notes)
				db.popNamespace()
				if len(open) == 0 {
					item = false
					break
				}
			}
		}
		if len(open) == 0 {
			return
		}
	}
}

func (ps *parser) relationEnd() RelationEnd {
	typ := END_TYPE[ps.types[ps.i]]
	if typ == 0 {
		return NONE
	}
	ps.i++
	return int(typ) - 1
}

func (ps *parser) classOrRelation() {
	db := ps.db
	id1 := ps.name(true)
	t := ps.types[ps.i]
	if t == T_LABEL {
		db.addMember(id1, db.cleanupLabel(ps.text()))
		return
	}
	title1 := "none"
	if t == T_STR {
		title1 = ps.text()
	} else if END_TYPE[t] == 0 && t != T_LINE && t != T_DOTTED_LINE {
		return
	}
	type1 := ps.relationEnd()
	t = ps.types[ps.i]
	if t != T_LINE && t != T_DOTTED_LINE {
		ps.fail("'AGGREGATION', 'EXTENSION', 'COMPOSITION', 'DEPENDENCY', 'LOLLIPOP', 'LINE', 'DOTTED_LINE'")
	}
	ps.i++
	type2 := ps.relationEnd()
	title2 := "none"
	if ps.types[ps.i] == T_STR {
		title2 = ps.text()
	}
	id2 := ps.name(true)
	lineType := 1
	if t == T_LINE {
		lineType = 0
	}
	relation := &ClassRelation{
		ID1:            id1,
		ID2:            id2,
		Relation:       Relation{Type1: type1, Type2: type2, LineType: lineType},
		RelationTitle1: title1,
		RelationTitle2: title2,
	}
	if ps.types[ps.i] == T_LABEL {
		title := db.cleanupLabel(ps.text())
		relation.Title = &title
	}
	db.addRelation(relation)
}

func (ps *parser) styles() []string {
	var out []string
	for {
		if STYLE_PART[ps.types[ps.i]] == 0 {
			ps.fail("'ALPHA', 'NUM', 'COLON', 'UNIT', 'SPACE', 'BRKT', 'STYLE', 'PCT', 'LABEL'")
		}
		s := ps.text()
		for STYLE_PART[ps.types[ps.i]] == 1 {
			s += ps.text()
		}
		out = append(out, s)
		if ps.types[ps.i] != T_COMMA {
			return out
		}
		ps.i++
	}
}

func (ps *parser) tooltip(id string) {
	if ps.types[ps.i] == T_STR {
		ps.db.setTooltip(id, ps.text())
	}
}

func (ps *parser) linkStatement(id string) {
	href := ps.expect(T_STR)
	var tip *string
	if ps.types[ps.i] == T_STR {
		t := ps.text()
		tip = &t
	}
	if ps.types[ps.i] == T_LINK_TARGET {
		target := ps.text()
		ps.db.setLink(id, href, &target)
	} else {
		ps.db.setLink(id, href, nil)
	}
	if tip != nil {
		ps.db.setTooltip(id, *tip)
	}
}

func (ps *parser) statement() {
	db := ps.db
	t := ps.types[ps.i]
	switch t {
	case T_UNICODE_TEXT, T_ALPHA, T_MINUS, T_BQUOTE_STR:
		ps.classOrRelation()
		return
	case T_NAMESPACE:
		ps.namespaceStatement()
		return
	case T_CLASS:
		ps.classStatement()
		return
	case T_MEMBER:
		ps.i++
		return
	case T_ANNOTATION_START:
		ps.i++
		annotation := ps.alphaNum()
		ps.expect(T_ANNOTATION_END)
		db.addAnnotation(ps.name(true), annotation)
		return
	case T_CALLBACK:
		ps.i++
		id := ps.name(true)
		ps.expect(T_STR)
		db.setClickEvent(id)
		ps.tooltip(id)
		return
	case T_LINK:
		ps.i++
		ps.linkStatement(ps.name(true))
		return
	case T_CLICK:
		ps.i++
		id := ps.name(true)
		if ps.types[ps.i] == T_HREF {
			ps.i++
			ps.linkStatement(id)
			return
		}
		ps.expect(T_CALLBACK_NAME)
		if ps.types[ps.i] == T_CALLBACK_ARGS {
			ps.i++
		}
		db.setClickEvent(id)
		ps.tooltip(id)
		return
	case T_STYLE:
		ps.i++
		id := ps.expect(T_ALPHA)
		db.setCssStyle(id, ps.styles())
		return
	case T_CSSCLASS:
		ps.i++
		ids := ps.expect(T_STR)
		db.setCssClass(ids, ps.expect(T_ALPHA))
		return
	case T_NOTE, T_NOTE_FOR:
		ps.noteStatement()
		return
	case T_CLASSDEF:
		ps.i++
		ids := []string{ps.expect(T_ALPHA)}
		for ps.types[ps.i] == T_COMMA {
			ps.i++
			ids = append(ids, ps.expect(T_ALPHA))
		}
		db.defineClass(ids, ps.styles())
		return
	case T_direction_tb, T_direction_bt, T_direction_rl, T_direction_lr:
		ps.i++
		db.setDirection(directionNames[t-T_direction_tb])
		return
	case T_acc_title:
		ps.i++
		db.setAccTitle(js.Trim(ps.expect(T_acc_title_value)))
		return
	case T_acc_descr:
		ps.i++
		db.setAccDescription(js.Trim(ps.expect(T_acc_descr_value)))
		return
	case T_acc_descr_multiline_value:
		db.setAccDescription(js.Trim(ps.text()))
		return
	}
	ps.fail(
		"'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'NAMESPACE', 'ANNOTATION_START', 'CLASS', 'MEMBER', 'NOTE_FOR', 'NOTE', 'CLASSDEF', 'direction_tb', 'CALLBACK', 'LINK', 'CLICK', 'STYLE', 'CSSCLASS', 'ALPHA', 'MINUS', 'BQUOTE_STR'",
	)
}

func parseClassDiagram(src string, db *ClassDb) {
	tokens := tokenize(src)
	// Padding past the end token, so that a look ahead never runs off the arrays.
	ps := &parser{
		src:    src,
		db:     db,
		types:  append(tokens.types, T_END, T_END),
		texts:  append(tokens.texts, "", ""),
		starts: append(tokens.starts, len(src), len(src)),
	}

	// Mermaid's grammar also accepts statements with no `classDiagram` line before them.
	header := ps.types[ps.i] == T_CLASS_DIAGRAM
	if header {
		ps.i++
		ps.expect(T_NEWLINE)
	}
	for {
		ps.statement()
		if ps.types[ps.i] != T_NEWLINE {
			break
		}
		ps.i++
		if ps.types[ps.i] == T_EOF || ps.types[ps.i] == T_END {
			break
		}
	}
	if header {
		ps.expect(T_EOF)
	}
	ps.expect(T_END)
}
