// Ported from pele src/diagrams/c4/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package c4

import (
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// KINDS: Mermaid's typeC4Shape for each shape keyword, indexed from T_PERSON.
var KINDS = [...]string{
	"person",
	"external_person",
	"system",
	"system_db",
	"system_queue",
	"external_system",
	"external_system_db",
	"external_system_queue",
	"container",
	"container_db",
	"container_queue",
	"external_container",
	"external_container_db",
	"external_container_queue",
	"component",
	"component_db",
	"component_queue",
	"external_component",
	"external_component_db",
	"external_component_queue",
}

var REL_TYPES = [...]string{"rel", "birel", "rel_u", "rel_d", "rel_l", "rel_r", "rel_b", "rel"}
var NODE_TYPES = [...]string{"node", "nodeL", "nodeR"}

const HEADERS = "'direction_tb', 'direction_bt', 'direction_rl', 'direction_lr', 'C4_CONTEXT', 'C4_CONTAINER', 'C4_COMPONENT', 'C4_DYNAMIC', 'C4_DEPLOYMENT'"
const ATTRIBUTES = "'STR', 'STR_KEY', 'ATTRIBUTE', 'ATTRIBUTE_EMPTY'"

const (
	// P_Item: a statement is required.
	P_Item = iota
	// P_Diagram: only a diagram statement may come next: the first one in a boundary, or one written straight after a title.
	P_Diagram
	P_ItemOrEnd
	P_AfterOther
	P_AfterDiagram
)

func isOther(t int) bool {
	return t == T_title || t == T_accDescription || t == T_acc_title || t == T_acc_descr || t == T_acc_descr_multiline_value
}

func isDiagram(t int) bool {
	return t >= T_PERSON && t <= T_UPDATE_LAYOUT_CONFIG
}

// insertAt is attrs.splice(at, 0, value): past the end, it appends.
func insertAt(attrs []C4Attr, at int, value C4Attr) []C4Attr {
	at = min(at, len(attrs))
	attrs = append(attrs, C4Attr{})
	copy(attrs[at+1:], attrs[at:])
	attrs[at] = value
	return attrs
}

// cutLeading is text.substring(k) where the text starts with k-1 ASCII characters and one
// whitespace character.
func cutLeading(s string, ascii int) string {
	return s[ascii+spaceAt(s, ascii):]
}

// parseC4 accepts what the parser Jison generates from c4Diagram.jison accepts, and calls the model
// builder in the order its semantic actions do. Boundaries nest through a counter, not recursion.
func parseC4(src string, db *C4Db) {
	toks := tokenize(src)
	typs, starts, ends := toks.types, toks.starts, toks.ends
	i := 0

	text := func(at int) string { return src[starts[at]:ends[at]] }
	fail := func(expected string) {
		t := typs[i]
		if t == T_ERROR {
			panic(types.SyntaxError("c4", src, starts[i], "", true))
		}
		panic(types.SyntaxError("c4", src, starts[i], "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
	}
	expect := func(typ int) {
		if typs[i] != typ {
			fail("'" + TOKEN_NAMES[typ] + "'")
		}
		i++
	}

	attributes := func() []C4Attr {
		// Room for every slot a statement has, and one inserted.
		attrs := make([]C4Attr, 0, 11)
		for {
			t := typs[i]
			if t == T_STR {
				attrs = append(attrs, C4Attr{str: js.Trim(text(i))})
				i++
			} else if t == T_ATTRIBUTE_EMPTY {
				attrs = append(attrs, C4Attr{})
				i++
			} else if t == T_STR_KEY {
				i++
				if typs[i] != T_STR_VALUE {
					fail("'STR_VALUE'")
				}
				attrs = append(attrs, C4Attr{named: &C4Named{key: js.Trim(text(i - 1)), value: js.Trim(text(i))}})
				i++
			} else {
				break
			}
		}
		if len(attrs) == 0 {
			fail(ATTRIBUTES)
		}
		return attrs
	}

	other := func(t int) {
		if t == T_title {
			db.setTitle(cutLeading(text(i), 5))
			i++
		} else if t == T_accDescription {
			db.setAccDescription(cutLeading(text(i), 14))
			i++
		} else if t == T_acc_descr_multiline_value {
			db.setAccDescription(js.Trim(text(i)))
			i++
		} else {
			i++
			if t == T_acc_title {
				if typs[i] != T_acc_title_value {
					fail("'acc_title_value'")
				}
				// The grammar hands `accTitle:` to setTitle, so it becomes the visible title.
				db.setTitle(js.Trim(text(i)))
				i++
			} else {
				if typs[i] != T_acc_descr_value {
					fail("'acc_descr_value'")
				}
				db.setAccDescription(js.Trim(text(i)))
				i++
			}
		}
	}

	// diagram reports whether the statement opens a boundary.
	diagram := func(t int) bool {
		i++
		attrs := attributes()
		if t <= T_SYSTEM_EXT_QUEUE {
			db.addPersonOrSystem(KINDS[t-T_PERSON], attrs)
		} else if t <= T_CONTAINER_EXT_QUEUE {
			db.addContainer(KINDS[t-T_PERSON], attrs)
		} else if t <= T_COMPONENT_EXT_QUEUE {
			db.addComponent(KINDS[t-T_PERSON], attrs)
		} else if t <= T_NODE_R {
			if t == T_ENTERPRISE_BOUNDARY {
				attrs = insertAt(attrs, 2, C4Attr{str: "ENTERPRISE"})
			} else if t == T_SYSTEM_BOUNDARY {
				attrs = insertAt(attrs, 2, C4Attr{str: "SYSTEM"})
			} else if t == T_CONTAINER_BOUNDARY {
				attrs = insertAt(attrs, 2, C4Attr{str: "CONTAINER"})
			}
			if t >= T_NODE {
				db.addDeploymentNode(NODE_TYPES[t-T_NODE], attrs)
			} else if t == T_CONTAINER_BOUNDARY {
				db.addContainerBoundary(attrs)
			} else {
				db.addPersonOrSystemBoundary(attrs, "system")
			}
			if typs[i] == T_LBRACE {
				i++
				expect(T_NEWLINE)
			} else if typs[i] == T_NEWLINE {
				i++
				expect(T_LBRACE)
				if typs[i] == T_NEWLINE {
					i++
				}
			} else {
				fail("'NEWLINE', 'LBRACE'")
			}
			return true
		} else if t <= T_REL_INDEX {
			// RelIndex takes a sequence number first, which Mermaid ignores.
			if t == T_REL_INDEX {
				attrs = attrs[1:]
			}
			db.addRel(REL_TYPES[t-T_REL], attrs)
		} else if t == T_UPDATE_EL_STYLE {
			db.updateElStyle(attrs)
		} else if t == T_UPDATE_REL_STYLE {
			db.updateRelStyle(attrs)
		} else {
			db.updateLayoutConfig(attrs)
		}
		return false
	}

	header := typs[i]
	if header < T_C4_CONTEXT || header > T_C4_DEPLOYMENT {
		fail(HEADERS)
	}
	i++
	expect(T_NEWLINE)

	depth := 0
	state := P_Item
	for {
		t := typs[i]
		if state == P_AfterOther || state == P_AfterDiagram {
			if t == T_NEWLINE {
				i++
				state = P_ItemOrEnd
				continue
			}
			if state == P_AfterOther && isDiagram(t) {
				state = P_Diagram
				continue
			}
		} else if isDiagram(t) {
			if diagram(t) {
				depth++
				state = P_Diagram
			} else {
				state = P_AfterDiagram
			}
			continue
		} else if isOther(t) && state != P_Diagram {
			other(t)
			state = P_AfterOther
			continue
		} else if state != P_ItemOrEnd {
			fail("'title', 'accDescription', 'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'PERSON', 'REL'")
		}

		if depth > 0 {
			if t != T_RBRACE {
				fail("'NEWLINE', 'RBRACE'")
			}
			i++
			db.popBoundaryParseStack()
			depth--
			state = P_AfterDiagram
		} else {
			if t != T_EOF {
				fail("'NEWLINE', 'EOF'")
			}
			i++
			expect(T_END)
			db.setC4Type(text(0))
			return
		}
	}
}
