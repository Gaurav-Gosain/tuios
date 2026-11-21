package ishikawa

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const STOP = "'SPACELINE', 'NL', 'EOF'"

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
