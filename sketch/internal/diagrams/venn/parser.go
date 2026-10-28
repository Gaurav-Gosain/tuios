// Ported from pele src/diagrams/venn/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package venn

import (
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const STATEMENT = "'EOF', 'NEWLINE', 'TITLE', 'SET', 'UNION', 'TEXT', 'INDENT_TEXT', 'STYLE'"
const VALUE = "'NUMERIC', 'IDENTIFIER', 'STRING', 'HEXCOLOR', 'RGBCOLOR', 'RGBACOLOR'"

// parseVenn ports Mermaid's venn grammar. A token is read only when the grammar has to look at one to
// decide, because the lexer asks the model whether an indented `text` continues a set, and the
// statements are what tell the model so.
func parseVenn(src string, db *VennDb) {
	lexer := &VennLexer{src: src, host: db}
	typ := -1

	peek := func() int {
		if typ == -1 {
			typ = lexer.next()
		}
		return typ
	}

	fail := func(expected string) {
		if peek() == T_ERROR {
			panic(types.SyntaxError("venn", src, lexer.start, "", true))
		}
		panic(types.SyntaxError("venn", src, lexer.start, "Expecting "+expected+", got '"+TOKEN_NAMES[typ]+"'", false))
	}

	take := func() string {
		typ = -1
		return lexer.text
	}

	expect := func(wanted int) string {
		if peek() != wanted {
			fail("'" + TOKEN_NAMES[wanted] + "'")
		}
		return take()
	}

	identifier := func() string {
		if peek() != T_IDENTIFIER && typ != T_STRING {
			fail("'IDENTIFIER', 'STRING'")
		}
		return take()
	}

	identifiers := func() []string {
		list := []string{identifier()}
		for peek() == T_COMMA {
			take()
			list = append(list, identifier())
		}
		return list
	}

	subset := func(ids []string, union bool) {
		label, hasLabel := "", false
		if peek() == T_BRACKET_LABEL {
			label, hasLabel = take(), true
		}
		size, hasSize := 0.0, false
		if peek() == T_COLON {
			take()
			size, hasSize = js.ParseFloat(expect(T_NUMERIC)), true
		}
		if union {
			if len(ids) < 2 {
				types.Throw("union requires multiple identifiers", types.CodeSemantic, types.ErrorInfo{Type: "venn"})
			}
			db.validateUnionIdentifiers(ids)
		}
		db.addSubsetData(ids, label, hasLabel, size, hasSize)
		db.setIndentMode(true)
	}

	// text reads the id of a text, and the label in brackets that an id which is not a number may carry.
	text := func(sets func() []string) {
		kind := peek()
		if kind != T_IDENTIFIER && kind != T_STRING && kind != T_NUMERIC {
			fail("'NUMERIC', 'IDENTIFIER', 'STRING'")
		}
		id := take()
		label, hasLabel := "", false
		if kind != T_NUMERIC && peek() == T_BRACKET_LABEL {
			label, hasLabel = take(), true
		}
		db.addTextData(sets(), id, label, hasLabel)
	}

	style := func() {
		ids := identifiers()
		var styles [][2]string
		for {
			key := expect(T_IDENTIFIER)
			expect(T_COLON)
			var value string
			if peek() == T_STRING {
				value = take()
			} else {
				var parts []string
				for peek() == T_IDENTIFIER || (typ >= T_NUMERIC && typ <= T_RGBACOLOR) {
					parts = append(parts, take())
				}
				if len(parts) == 0 {
					fail(VALUE)
				}
				value = joinSpace(parts)
			}
			styles = append(styles, [2]string{key, value})
			if peek() != T_COMMA {
				break
			}
			take()
		}
		db.addStyleData(ids, styles)
	}

	for peek() == T_NEWLINE {
		take()
	}
	expect(T_VENN)
	for {
		statement := peek()
		if statement == T_EOF {
			return
		}
		if statement < T_NEWLINE || statement > T_STYLE || statement == T_VENN {
			fail(STATEMENT)
		}
		take()
		switch statement {
		case T_TITLE:
			db.setDiagramTitle(lexer.text[lexer.titleSkip:])
		case T_SET:
			subset([]string{identifier()}, false)
		case T_UNION:
			subset(identifiers(), true)
		case T_TEXT:
			ids := identifiers()
			text(func() []string { return ids })
		case T_INDENT_TEXT:
			text(func() []string {
				if !db.hasCurrent {
					types.Throw("text requires set", types.CodeSemantic, types.ErrorInfo{Type: "venn"})
				}
				return db.current
			})
		case T_STYLE:
			style()
		}
	}
}

func joinSpace(parts []string) string {
	n := len(parts) - 1
	for _, p := range parts {
		n += len(p)
	}
	b := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, p...)
	}
	return string(b)
}
