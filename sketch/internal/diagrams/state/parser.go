// Ported from pele src/diagrams/state/parser.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// pele's StateBuilder interface, the calls Mermaid's grammar makes on its
// `yy` object, is the *StateDb here: it has no other implementation.

package state

import (
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

var DIRECTIONS = [...]string{"TB", "BT", "RL", "LR"}

const STATEMENT = "'NL', 'ID', 'HIDE_EMPTY', 'scale', 'COMPOSIT_STATE', 'STATE_DESCR', 'FORK', 'JOIN', 'CHOICE', 'CONCURRENT', 'note', " +
	"'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'CLICK', 'classDef', 'style', 'class', 'direction_tb', " +
	"'direction_bt', 'direction_rl', 'direction_lr', 'EDGE_STATE'"

func parseState(src string, db *StateDb) []Stmt {
	tokens := tokenize(src)
	// Padding past the end token, so that a look ahead never runs off the arrays.
	typs := append(tokens.types, T_END, T_END)
	texts := append(tokens.texts, "", "")
	starts := append(tokens.starts, len(src), len(src))
	i := 0

	fail := func(expected string) {
		t := typs[i]
		if t == T_ERROR {
			if tokens.error != "" {
				loc := types.Locate(src, starts[i])
				types.Throw(tokens.error, types.CodeSyntax, types.ErrorInfo{Type: "state", Line: loc.Line, Column: loc.Column, Snippet: loc.Snippet})
			}
			panic(types.SyntaxError("state", src, starts[i], "", true))
		}
		panic(types.SyntaxError("state", src, starts[i], "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
	}
	expect := func(typ int) string {
		if typs[i] != typ {
			fail("'" + TOKEN_NAMES[typ] + "'")
		}
		text := texts[i]
		i++
		return text
	}
	idStatement := func() *StateStmt {
		t := typs[i]
		if t != T_ID && t != T_EDGE_STATE {
			fail("'ID', 'EDGE_STATE'")
		}
		id := js.Trim(texts[i])
		i++
		if typs[i] != T_STYLE_SEPARATOR {
			return &StateStmt{ID: id, Type: "default", Description: &Description{}}
		}
		i++
		return &StateStmt{ID: id, Classes: []string{js.Trim(expect(T_ID))}, Type: "default", Description: &Description{}}
	}

	for typs[i] == T_NL {
		i++
	}
	if typs[i] != T_SD {
		fail("'NL', 'SD'")
	}
	i++

	doc := &[]Stmt{}
	var outer []*[]Stmt
	// The grammar drops a statement whose value is the string it uses for a blank line.
	add := func(value Stmt) {
		if s, ok := value.(string); ok && s == "nl" {
			return
		}
		*doc = append(*doc, value)
	}
	open := func(id, description string) {
		i++
		inner := &[]Stmt{}
		*doc = append(*doc, &StateStmt{ID: id, Type: "default", Description: &Description{Text: description}, Doc: inner})
		outer = append(outer, doc)
		doc = inner
	}
	text := func() string {
		t := texts[i]
		i++
		return t
	}

	for {
		t := typs[i]
		switch t {
		case T_NL:
			i++
		case T_ID, T_EDGE_STATE:
			first := idStatement()
			if typs[i] == T_DESCR {
				first.Description = &Description{Text: db.trimColon(text())}
				add(first)
			} else if typs[i] == T_ARROW {
				i++
				second := idStatement()
				if typs[i] == T_DESCR {
					description := db.trimColon(text())
					add(&RelationStmt{State1: first, State2: second, Description: &description})
				} else {
					add(&RelationStmt{State1: first, State2: second})
				}
			} else {
				add(first)
			}
		case T_HIDE_EMPTY:
			add(text())
		case T_scale:
			i++
			expect(T_WIDTH)
			add(texts[i-2])
		case T_COMPOSIT_STATE:
			if typs[i+1] == T_STRUCT_START {
				open(text(), "")
			} else {
				add(text())
			}
		case T_STATE_DESCR:
			description := text()
			expect(T_AS)
			id := expect(T_ID)
			if typs[i] == T_STRUCT_START {
				open(id, description)
			} else if strings.Contains(id, ":") {
				parts := strings.Split(id, ":")
				add(&StateStmt{ID: parts[0], Type: "default", Description: &Description{IsList: true, List: []string{js.Trim(description), parts[1]}}})
			} else {
				add(&StateStmt{ID: id, Type: "default", Description: &Description{Text: js.Trim(description)}})
			}
		case T_FORK:
			add(&StateStmt{ID: text(), Type: "fork"})
		case T_JOIN:
			add(&StateStmt{ID: text(), Type: "join"})
		case T_CHOICE:
			add(&StateStmt{ID: text(), Type: "choice"})
		case T_CONCURRENT:
			i++
			add(&StateStmt{ID: db.getDividerId(), Type: "divider"})
		case T_note:
			keyword := text()
			if typs[i] == T_left_of || typs[i] == T_right_of {
				position := js.Trim(text())
				id := js.Trim(expect(T_ID))
				add(&StateStmt{ID: id, Note: &StateNote{Position: position, Text: js.Trim(expect(T_NOTE_TEXT))}})
			} else {
				if typs[i] != T_NOTE_TEXT {
					fail("'NOTE_TEXT', 'left_of', 'right_of'")
				}
				i++
				expect(T_AS)
				expect(T_ID)
				add(keyword)
			}
		case T_direction_tb, T_direction_bt, T_direction_rl, T_direction_lr:
			value := DIRECTIONS[t-T_direction_tb]
			i++
			db.setDirection(value)
			add(&DirectionStmt{value})
		case T_acc_title:
			i++
			value := js.Trim(expect(T_acc_title_value))
			db.setAccTitle(value)
			add(value)
		case T_acc_descr:
			i++
			value := js.Trim(expect(T_acc_descr_value))
			db.setAccDescription(value)
			add(value)
		case T_acc_descr_multiline_value:
			value := js.Trim(text())
			db.setAccDescription(value)
			add(value)
		case T_CLICK:
			i++
			id := idStatement()
			var url string
			tooltip := ""
			if typs[i] == T_HREF {
				i++
				url = expect(T_STRING)
			} else {
				if typs[i] != T_STRING {
					fail("'STRING', 'HREF'")
				}
				url = text()
				tooltip = expect(T_STRING)
			}
			expect(T_NL)
			add(&ClickStmt{ID: id, URL: url, Tooltip: tooltip})
		case T_classDef:
			i++
			id := js.Trim(expect(T_CLASSDEF_ID))
			add(&ClassDefStmt{ID: id, Classes: js.Trim(expect(T_CLASSDEF_STYLEOPTS))})
		case T_style:
			i++
			id := js.Trim(expect(T_STYLE_IDS))
			add(&StyleStmt{ID: id, StyleClass: js.Trim(expect(T_STYLEDEF_STYLEOPTS))})
		case T_class:
			i++
			id := js.Trim(expect(T_CLASSENTITY_IDS))
			add(&ApplyClassStmt{ID: id, StyleClass: js.Trim(expect(T_STYLECLASS))})
		case T_STRUCT_STOP:
			if len(outer) == 0 {
				fail("'$end', " + STATEMENT)
			}
			i++
			doc = outer[len(outer)-1]
			outer = outer[:len(outer)-1]
		case T_END:
			if len(outer) > 0 {
				fail("'STRUCT_STOP', " + STATEMENT)
			}
			db.setRootDoc(*doc)
			return *doc
		default:
			if len(outer) > 0 {
				fail("'STRUCT_STOP', " + STATEMENT)
			}
			fail("'$end', " + STATEMENT)
		}
	}
}
