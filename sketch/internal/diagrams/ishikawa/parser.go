// Ported from pele src/diagrams/ishikawa/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package ishikawa

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const STOP = "'SPACELINE', 'NL', 'EOF'"

// parseIshikawa follows the parser Jison builds from Mermaid's grammar: blank lines and comments may come first,
// then the keyword and at most one line break, then statements. A statement is a line of text,
// with or without indentation, or a blank line; each is ended by a blank line, a line break or
// the end of the text, and any number of line breaks after that.
func parseIshikawa(src string, db *IshikawaDb) {
	tokens := tokenize(src)
	typesOf, starts, ends := tokens.types, tokens.starts, tokens.ends
	i := 0

	fail := func(expected string) {
		panic(types.SyntaxError("ishikawa", src, starts[i], "Expecting "+expected+", got '"+TOKEN_NAMES[typesOf[i]]+"'", false))
	}

	if typesOf[i] == T_SPACELINE {
		i++
		for typesOf[i] == T_SPACELINE || typesOf[i] == T_NL {
			i++
		}
		if typesOf[i] != T_ISHIKAWA {
			fail("'SPACELINE', 'NL', 'ISHIKAWA'")
		}
	} else if typesOf[i] != T_ISHIKAWA {
		fail("'SPACELINE', 'ISHIKAWA'")
	}
	i++
	if typesOf[i] == T_NL {
		i++
	}

	for {
		typ := typesOf[i]
		if typ == T_TEXT || (typ == T_SPACELIST && typesOf[i+1] == T_TEXT) {
			text := i
			if typ != T_TEXT {
				text = i + 1
			}
			i = text + 1
			if typesOf[i] != T_SPACELINE && typesOf[i] != T_NL && typesOf[i] != T_EOF {
				fail(STOP)
			}
			level := 0
			if typ != T_TEXT {
				// The run is white space only, each character one UTF-16 unit.
				level = utf8.RuneCountInString(src[starts[text-1]:ends[text-1]])
			}
			db.addNode(level, js.Trim(src[starts[text]:ends[text]]))
		} else if typ == T_SPACELINE || typ == T_SPACELIST {
			i++
			if typesOf[i] != T_SPACELINE && typesOf[i] != T_NL && typesOf[i] != T_EOF {
				if typ == T_SPACELIST {
					fail(STOP + ", 'TEXT'")
				} else {
					fail(STOP)
				}
			}
		} else {
			fail("'SPACELINE', 'SPACELIST', 'TEXT'")
		}
		i++
		for typesOf[i] == T_NL || typesOf[i] == T_EOF {
			i++
		}
		if typesOf[i] == T_END {
			return
		}
	}
}
