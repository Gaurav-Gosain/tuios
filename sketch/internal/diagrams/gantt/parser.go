// Ported from pele src/diagrams/gantt/parser.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// GanttBuilder is the GanttDb itself here. pele's WEEKDAYS here is the lexer's DAYS, the same
// list, as dates.go has a WEEKDAYS of its own.

package gantt

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const LINE = "'EOF', 'SPACE', 'NL', 'weekday_monday', 'weekday_tuesday', 'weekday_wednesday', 'weekday_thursday', " +
	"'weekday_friday', 'weekday_saturday', 'weekday_sunday', 'weekend_friday', 'weekend_saturday', 'dateFormat', " +
	"'inclusiveEndDates', 'topAxis', 'axisFormat', 'tickInterval', 'excludes', 'includes', 'todayMarker', 'title', " +
	"'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'section', 'taskTxt', 'click'"

// Tokens that can start a line, which are also the only ones that can follow a complete statement.
var STARTS_LINE = func() (starts [T_EOF + 1]bool) {
	for t := T_weekday_monday; t <= T_weekend_saturday; t++ {
		starts[t] = true
	}
	for _, t := range []int{
		T_EOF,
		T_NL,
		T_dateFormat,
		T_inclusiveEndDates,
		T_topAxis,
		T_axisFormat,
		T_tickInterval,
		T_excludes,
		T_includes,
		T_todayMarker,
		T_title,
		T_acc_title,
		T_acc_descr,
		T_acc_descr_multiline_value,
		T_section,
		T_taskTxt,
		T_click,
	} {
		starts[t] = true
	}
	return
}()

// sliceFrom is text.slice(n): the text after its first n UTF-16 units.
func sliceFrom(text string, n int) string {
	q := 0
	for n > 0 && q < len(text) {
		r, size := utf8.DecodeRuneInString(text[q:])
		if r > 0xffff {
			n--
		}
		n--
		q += size
	}
	return text[q:]
}

type parser struct {
	src    string
	types  []int
	texts  []string
	starts []int
	i      int
}

func (ps *parser) fail(expected string) {
	t := ps.types[ps.i]
	got := "1"
	if t != T_END {
		got = TOKEN_NAMES[t]
	}
	panic(types.SyntaxError("gantt", ps.src, ps.starts[ps.i], "Expecting "+expected+", got '"+got+"'", false))
}

// Mermaid's parser runs a statement's action only after seeing that the next token can follow it.
//
//go:noinline
func (ps *parser) end(also string) {
	if !STARTS_LINE[ps.types[ps.i]] {
		ps.fail(LINE + also)
	}
}

func parseGantt(src string, db *GanttDb) {
	toks := tokenize(src)
	// Padding, so that a look past the last token reads the end.
	ps := &parser{src: src, types: append(toks.types, T_END, T_END), texts: append(toks.texts, "", ""), starts: append(toks.starts, len(src), len(src))}

	if ps.types[ps.i] != T_gantt {
		ps.fail("'gantt'")
	}
	ps.i++

	for {
		t := ps.types[ps.i]
		text := ps.texts[ps.i]
		if t > T_EOF || !STARTS_LINE[t] {
			ps.fail(LINE)
		}
		ps.i++
		switch t {
		case T_EOF:
			if ps.types[ps.i] == T_END {
				return
			}
			ps.end("")
		case T_NL:
		case T_dateFormat:
			ps.end("")
			db.setDateFormat(sliceFrom(text, 11))
		case T_inclusiveEndDates:
			ps.end("")
			db.enableInclusiveEndDates()
		case T_topAxis:
			ps.end("")
			db.TopAxis()
		case T_axisFormat:
			ps.end("")
			db.setAxisFormat(sliceFrom(text, 11))
		case T_tickInterval:
			ps.end("")
			db.setTickInterval(sliceFrom(text, 13))
		case T_excludes:
			ps.end("")
			db.setExcludes(sliceFrom(text, 9))
		case T_includes:
			ps.end("")
			db.setIncludes(sliceFrom(text, 9))
		case T_todayMarker:
			ps.end("")
			db.setTodayMarker(sliceFrom(text, 12))
		case T_weekend_friday:
			ps.end("")
			db.setWeekend("friday")
		case T_weekend_saturday:
			ps.end("")
			db.setWeekend("saturday")
		case T_title:
			ps.end("")
			db.setDiagramTitle(sliceFrom(text, 6))
		case T_acc_title:
			if ps.types[ps.i] != T_acc_title_value {
				ps.fail("'acc_title_value'")
			}
			value := ps.texts[ps.i]
			ps.i++
			ps.end("")
			db.setAccTitle(js.Trim(value))
		case T_acc_descr:
			if ps.types[ps.i] != T_acc_descr_value {
				ps.fail("'acc_descr_value'")
			}
			value := ps.texts[ps.i]
			ps.i++
			ps.end("")
			db.setAccDescription(js.Trim(value))
		case T_acc_descr_multiline_value:
			ps.end("")
			db.setAccDescription(js.Trim(text))
		case T_section:
			ps.end("")
			db.addSection(sliceFrom(text, 8))
		case T_taskTxt:
			if ps.types[ps.i] != T_taskData {
				ps.fail("'taskData'")
			}
			data := ps.texts[ps.i]
			ps.i++
			ps.end("")
			db.addTask(text, data)
		case T_click:
			var name, args, href *string
			if ps.types[ps.i] == T_callbackname {
				name = &ps.texts[ps.i]
				ps.i++
				if ps.types[ps.i] == T_callbackargs {
					args = &ps.texts[ps.i]
					ps.i++
				}
				if ps.types[ps.i] == T_href {
					href = &ps.texts[ps.i]
					ps.i++
					ps.end("")
				} else if args == nil {
					ps.end(", 'callbackargs', 'href'")
				} else {
					ps.end(", 'href'")
				}
			} else if ps.types[ps.i] == T_href {
				href = &ps.texts[ps.i]
				ps.i++
				if ps.types[ps.i] == T_callbackname {
					name = &ps.texts[ps.i]
					ps.i++
					if ps.types[ps.i] == T_callbackargs {
						args = &ps.texts[ps.i]
						ps.i++
						ps.end("")
					} else {
						ps.end(", 'callbackargs'")
					}
				} else {
					ps.end(", 'callbackname'")
				}
			} else {
				ps.fail("'callbackname', 'href'")
			}
			if name != nil {
				db.setClickEvent(text, *name, args)
			}
			if href != nil {
				db.setLink(text, *href)
			}
		default:
			ps.end("")
			db.setWeekday(DAYS[t-T_weekday_monday])
		}
	}
}
