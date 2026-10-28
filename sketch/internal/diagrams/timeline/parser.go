// Ported from pele src/diagrams/timeline/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package timeline

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const STATEMENT = "'EOF', 'SPACE', 'NEWLINE', 'title', 'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'section', 'period', 'event'"

// substring is text.substring(k) for a text that starts with ASCII and one whitespace
// character: k counts UTF-16 units, and the character the cut falls after is in the BMP.
func substring(text string, k int) string {
	i := 0
	for ; k > 0 && i < len(text); k-- {
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
	return text[i:]
}

func parseTimeline(src string, db *TimelineDb) {
	toks := tokenize(src)
	typ := func(i int) int {
		if i >= len(toks.Types) {
			return -1
		}
		return toks.Types[i]
	}

	fail := func(i int, expected string) {
		got := "undefined"
		start := len(src)
		if t := typ(i); t == T_END {
			got = "1"
			start = toks.Starts[i]
		} else if t >= 0 {
			got = TOKEN_NAMES[t]
			start = toks.Starts[i]
		}
		panic(types.SyntaxError("timeline", src, start, "Expecting "+expected+", got '"+got+"'", false))
	}

	head := typ(0)
	if head != T_timeline && head != T_timeline_lr && head != T_timeline_td {
		fail(0, "'timeline', 'timeline_lr', 'timeline_td'")
	}
	if head != T_timeline {
		if head == T_timeline_lr {
			db.setDirection("LR")
		} else {
			db.setDirection("TD")
		}
	}

	for i := 1; ; i++ {
		var text string
		if i < len(toks.Texts) {
			text = toks.Texts[i]
		}
		switch typ(i) {
		case T_NEWLINE:
		case T_title:
			db.setDiagramTitle(substring(text, 6))
		case T_acc_title:
			i++
			db.setAccTitle(js.Trim(toks.Texts[i]))
		case T_acc_descr:
			i++
			db.setAccDescription(js.Trim(toks.Texts[i]))
		case T_acc_descr_multiline_value:
			db.setAccDescription(js.Trim(text))
		case T_section:
			db.addSection(substring(text, 8))
		case T_period:
			db.addTask(text, 0, "")
		case T_event:
			db.addEvent(substring(text, 2))
		case T_EOF:
			return
		default:
			fail(i, STATEMENT)
		}
	}
}
