// Ported from pele src/diagrams/sequence/parser.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package sequence

import (
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

var SIGNAL = func() [T_STICK_ARROW_BOTTOM_REVERSE_DOTTED + 1]int {
	var s [T_STICK_ARROW_BOTTOM_REVERSE_DOTTED + 1]int
	for i := range s {
		s[i] = -1
	}
	s[T_SOLID_OPEN_ARROW] = LINETYPE_SOLID_OPEN
	s[T_DOTTED_OPEN_ARROW] = LINETYPE_DOTTED_OPEN
	s[T_SOLID_ARROW] = LINETYPE_SOLID
	s[T_DOTTED_ARROW] = LINETYPE_DOTTED
	s[T_BIDIRECTIONAL_SOLID_ARROW] = LINETYPE_BIDIRECTIONAL_SOLID
	s[T_BIDIRECTIONAL_DOTTED_ARROW] = LINETYPE_BIDIRECTIONAL_DOTTED
	s[T_SOLID_CROSS] = LINETYPE_SOLID_CROSS
	s[T_DOTTED_CROSS] = LINETYPE_DOTTED_CROSS
	s[T_SOLID_POINT] = LINETYPE_SOLID_POINT
	s[T_DOTTED_POINT] = LINETYPE_DOTTED_POINT
	s[T_SOLID_ARROW_TOP] = LINETYPE_SOLID_TOP
	s[T_SOLID_ARROW_BOTTOM] = LINETYPE_SOLID_BOTTOM
	s[T_STICK_ARROW_TOP] = LINETYPE_STICK_TOP
	s[T_STICK_ARROW_BOTTOM] = LINETYPE_STICK_BOTTOM
	s[T_SOLID_ARROW_TOP_DOTTED] = LINETYPE_SOLID_TOP_DOTTED
	s[T_SOLID_ARROW_BOTTOM_DOTTED] = LINETYPE_SOLID_BOTTOM_DOTTED
	s[T_STICK_ARROW_TOP_DOTTED] = LINETYPE_STICK_TOP_DOTTED
	s[T_STICK_ARROW_BOTTOM_DOTTED] = LINETYPE_STICK_BOTTOM_DOTTED
	s[T_SOLID_ARROW_TOP_REVERSE] = LINETYPE_SOLID_ARROW_TOP_REVERSE
	s[T_SOLID_ARROW_BOTTOM_REVERSE] = LINETYPE_SOLID_ARROW_BOTTOM_REVERSE
	s[T_STICK_ARROW_TOP_REVERSE] = LINETYPE_STICK_ARROW_TOP_REVERSE
	s[T_STICK_ARROW_BOTTOM_REVERSE] = LINETYPE_STICK_ARROW_BOTTOM_REVERSE
	s[T_SOLID_ARROW_TOP_REVERSE_DOTTED] = LINETYPE_SOLID_ARROW_TOP_REVERSE_DOTTED
	s[T_SOLID_ARROW_BOTTOM_REVERSE_DOTTED] = LINETYPE_SOLID_ARROW_BOTTOM_REVERSE_DOTTED
	s[T_STICK_ARROW_TOP_REVERSE_DOTTED] = LINETYPE_STICK_ARROW_TOP_REVERSE_DOTTED
	s[T_STICK_ARROW_BOTTOM_REVERSE_DOTTED] = LINETYPE_STICK_ARROW_BOTTOM_REVERSE_DOTTED
	return s
}()

// Block kinds.
const (
	K_Top = iota
	K_Box
	K_Loop
	K_Rect
	K_Opt
	K_Break
	K_Alt
	K_Par
	K_Critical
)

type frame struct {
	kind      int
	text      string
	startType int
	// list is the current section's statements. Slot 0 of a block's first section is kept for its start entry.
	list []SeqItem
	// sections are the finished sections, each followed by the divider text that ended it.
	sections []sectionEnd
}

type sectionEnd struct {
	list []SeqItem
	text string
}

var BLOCK = func() [T_STICK_ARROW_BOTTOM_REVERSE_DOTTED + 1]int {
	var b [T_STICK_ARROW_BOTTOM_REVERSE_DOTTED + 1]int
	for i := range b {
		b[i] = -1
	}
	b[T_loop] = K_Loop
	b[T_rect] = K_Rect
	b[T_opt] = K_Opt
	b[T_break] = K_Break
	b[T_alt] = K_Alt
	b[T_par] = K_Par
	b[T_par_over] = K_Par
	b[T_critical] = K_Critical
	b[T_box] = K_Box
	return b
}()

const SIGNAL_TOKENS = "'SOLID_OPEN_ARROW', 'DOTTED_OPEN_ARROW', 'SOLID_ARROW', 'DOTTED_ARROW', 'SOLID_CROSS', 'DOTTED_CROSS', 'SOLID_POINT', 'DOTTED_POINT'"

func parseSequence(src string, db *SeqDb) {
	tk := tokenize(src)
	typesT, texts, starts := tk.types, tk.texts, tk.starts
	// pele reads past the last token as undefined. Padding with END keeps the port's reads in range.
	for k := 0; k < 4; k++ {
		typesT = append(typesT, T_END)
		texts = append(texts, "")
		starts = append(starts, len(src))
	}
	i := 0

	fail := func(expected string) {
		t := typesT[i]
		if t == T_ERROR {
			panic(types.SyntaxError("sequence", src, starts[i], "", true))
		}
		panic(types.SyntaxError("sequence", src, starts[i], "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
	}
	expect := func(typ int) string {
		if typesT[i] != typ {
			fail("'" + TOKEN_NAMES[typ] + "'")
		}
		s := texts[i]
		i++
		return s
	}
	actor := func() *SeqStatement {
		t := typesT[i]
		if t != T_ACTOR && t != T_link {
			fail("'ACTOR'")
		}
		s := &SeqStatement{Type: "addParticipant", Actor: texts[i]}
		i++
		return s
	}
	text2 := func() *SeqText {
		t := js.Trim(expect(T_TXT))
		_, size := utf8.DecodeRuneInString(t)
		if t == "" {
			size = 0
		}
		return db.ParseMessage(t[size:])
	}

	participant := func() *SeqStatement {
		t := typesT[i]
		i++
		if t == T_destroy {
			node := actor()
			expect(T_NEWLINE)
			node.Type = "destroyParticipant"
			return node
		}
		node := actor()
		if typesT[i] == T_CONFIG_START && typesT[i-1] == T_ACTOR {
			i++
			content := expect(T_CONFIG_CONTENT)
			expect(T_CONFIG_END)
			c := js.Trim(content)
			node.Config = &c
		}
		if t == T_participant {
			node.Draw = "participant"
		} else {
			node.Draw = "actor"
		}
		if typesT[i] == T_AS {
			i++
			alias := expect(T_restOfLine)
			expect(T_NEWLINE)
			node.Description = db.ParseMessage(alias)
		} else if typesT[i] == T_NEWLINE {
			i++
		} else {
			fail("'AS', 'NEWLINE'")
		}
		return node
	}

	signal := func(from *SeqStatement) SeqItem {
		centralFrom := false
		if typesT[i] == T_CENTRAL {
			centralFrom = true
			i++
		}
		signalType := SIGNAL[min(typesT[i], len(SIGNAL)-1)]
		if typesT[i] >= len(SIGNAL) {
			signalType = -1
		}
		if signalType < 0 {
			fail(SIGNAL_TOKENS)
		}
		i++
		mark := typesT[i]
		if mark == T_CENTRAL || (!centralFrom && (mark == T_PLUS || mark == T_MINUS)) {
			i++
		}
		to := actor()
		msg := text2()
		message := &SeqStatement{Type: "addMessage", From: from.Actor, To: to.Actor, SignalType: signalType, Msg: msg}
		out := []SeqItem{from, to, message}
		if centralFrom {
			dual := mark == T_CENTRAL
			message.Activate = dual
			if dual {
				message.CentralConnection = LINETYPE_CENTRAL_CONNECTION_DUAL
			} else {
				message.CentralConnection = LINETYPE_CENTRAL_CONNECTION_REVERSE
			}
			if dual {
				out = append(out, &SeqStatement{Type: "centralConnection", SignalType: LINETYPE_CENTRAL_CONNECTION, Actor: to.Actor})
			}
			out = append(out, &SeqStatement{Type: "centralConnectionReverse", SignalType: LINETYPE_CENTRAL_CONNECTION_REVERSE, Actor: from.Actor})
		} else if mark == T_PLUS {
			message.Activate = true
			out = append(out, &SeqStatement{Type: "activeStart", SignalType: LINETYPE_ACTIVE_START, Actor: to.Actor})
		} else if mark == T_MINUS {
			out = append(out, &SeqStatement{Type: "activeEnd", SignalType: LINETYPE_ACTIVE_END, Actor: from.Actor})
		} else if mark == T_CENTRAL {
			message.Activate = true
			message.CentralConnection = LINETYPE_CENTRAL_CONNECTION
			out = append(out, &SeqStatement{Type: "centralConnection", SignalType: LINETYPE_CENTRAL_CONNECTION, Actor: to.Actor})
		}
		return out
	}

	menu := func(typ string) SeqItem {
		i++
		who := actor()
		return []SeqItem{who, &SeqStatement{Type: typ, Actor: who.Actor, Text: text2()}}
	}

	note := func() SeqItem {
		i++
		t := typesT[i]
		if t == T_over {
			i++
			first := actor()
			var who SeqItem = first
			ids := []string{first.Actor, first.Actor}
			if typesT[i] == T_COMMA {
				i++
				second := actor()
				who = []SeqItem{first, second}
				ids = []string{first.Actor, second.Actor}
			}
			return []SeqItem{who, &SeqStatement{Type: "addNote", Placement: PLACEMENT_OVER, ActorPair: ids, Text: text2()}}
		}
		if t != T_left_of && t != T_right_of {
			fail("'over', 'left_of', 'right_of'")
		}
		i++
		who := actor()
		placement := PLACEMENT_RIGHTOF
		if t == T_left_of {
			placement = PLACEMENT_LEFTOF
		}
		return []SeqItem{who, &SeqStatement{Type: "addNote", Placement: placement, Actor: who.Actor, Text: text2()}}
	}

	autonumber := func() *SeqStatement {
		i++
		node := &SeqStatement{Type: "sequenceIndex", SequenceVisible: true, SignalType: LINETYPE_AUTONUMBER}
		if typesT[i] == T_NUM {
			node.SequenceIndex = js.Number(texts[i])
			i++
			if typesT[i] == T_NUM {
				node.SequenceIndexStep = js.Number(texts[i])
				i++
			} else {
				node.SequenceIndexStep = 1
			}
		} else if typesT[i] == T_off {
			i++
			node.SequenceVisible = false
		}
		if typesT[i] != T_NEWLINE {
			fail("'NEWLINE', 'NUM', 'off'")
		}
		i++
		return node
	}

	// fold folds the sections of an alt, par, or critical block from the last one back, as the grammar's
	// right recursion does, so the texts reach the model builder in the same order.
	fold := func(f *frame, divider string, key string, signalType int) []SeqItem {
		list := f.list
		for k := len(f.sections) - 1; k >= 0; k-- {
			head := &SeqStatement{Type: divider, SignalType: signalType}
			t := db.ParseMessage(f.sections[k].text)
			switch key {
			case "altText":
				head.AltText = t
			case "parText":
				head.ParText = t
			default:
				head.OptionText = t
			}
			next := append([]SeqItem{}, f.sections[k].list...)
			list = append(next, head, list)
		}
		return list
	}

	closeFrame := func(f *frame) []SeqItem {
		t := f.text
		list := f.list
		switch f.kind {
		case K_Box:
			list[0] = &SeqStatement{Type: "boxStart", BoxData: db.ParseBoxData(t)}
			list = append(list, &SeqStatement{Type: "boxEnd", BoxText: t})
		case K_Loop:
			list[0] = &SeqStatement{Type: "loopStart", LoopText: db.ParseMessage(t), SignalType: LINETYPE_LOOP_START}
			list = append(list, &SeqStatement{Type: "loopEnd", LoopText: &SeqText{Text: t}, SignalType: LINETYPE_LOOP_END})
		case K_Rect:
			list[0] = &SeqStatement{Type: "rectStart", Color: db.ParseMessage(t), SignalType: LINETYPE_RECT_START}
			list = append(list, &SeqStatement{Type: "rectEnd", Color: db.ParseMessage(t), SignalType: LINETYPE_RECT_END})
		case K_Opt:
			list[0] = &SeqStatement{Type: "optStart", OptText: db.ParseMessage(t), SignalType: LINETYPE_OPT_START}
			list = append(list, &SeqStatement{Type: "optEnd", OptText: db.ParseMessage(t), SignalType: LINETYPE_OPT_END})
		case K_Break:
			list[0] = &SeqStatement{Type: "breakStart", BreakText: db.ParseMessage(t), SignalType: LINETYPE_BREAK_START}
			list = append(list, &SeqStatement{Type: "breakEnd", OptText: db.ParseMessage(t), SignalType: LINETYPE_BREAK_END})
		case K_Alt:
			list = fold(f, "else", "altText", LINETYPE_ALT_ELSE)
			list[0] = &SeqStatement{Type: "altStart", AltText: db.ParseMessage(t), SignalType: LINETYPE_ALT_START}
			list = append(list, &SeqStatement{Type: "altEnd", SignalType: LINETYPE_ALT_END})
		case K_Par:
			list = fold(f, "and", "parText", LINETYPE_PAR_AND)
			list[0] = &SeqStatement{Type: "parStart", ParText: db.ParseMessage(t), SignalType: f.startType}
			list = append(list, &SeqStatement{Type: "parEnd", SignalType: LINETYPE_PAR_END})
		default:
			list = fold(f, "option", "optionText", LINETYPE_CRITICAL_OPTION)
			list[0] = &SeqStatement{Type: "criticalStart", CriticalText: db.ParseMessage(t), SignalType: LINETYPE_CRITICAL_START}
			list = append(list, &SeqStatement{Type: "criticalEnd", SignalType: LINETYPE_CRITICAL_END})
		}
		return list
	}

	for typesT[i] == T_NEWLINE {
		i++
	}
	expect(T_SD)

	var stack []*frame
	f := &frame{kind: K_Top, list: []SeqItem{}}

	for {
		t := typesT[i]
		if f.kind == K_Box {
			switch {
			case t == T_participant || t == T_participant_actor || t == T_destroy:
				f.list = append(f.list, participant())
			case t == T_NEWLINE:
				f.list = append(f.list, []SeqItem{})
				i++
			case t == T_end:
				i++
				done := closeFrame(f)
				f = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				f.list = append(f.list, done)
			default:
				fail("'end', 'NEWLINE', 'participant', 'participant_actor', 'destroy'")
			}
			continue
		}
		switch t {
		case T_NEWLINE, T_INVALID:
			f.list = append(f.list, []SeqItem{})
			i++
		case T_participant, T_participant_actor, T_destroy:
			f.list = append(f.list, participant())
		case T_create:
			i++
			next := typesT[i]
			if next != T_participant && next != T_participant_actor && next != T_destroy {
				fail("'participant', 'participant_actor', 'destroy'")
			}
			node := participant()
			node.Type = "createParticipant"
			f.list = append(f.list, node)
		case T_ACTOR:
			f.list = append(f.list, signal(actor()))
			expect(T_NEWLINE)
		case T_link:
			next := typesT[i+1]
			if next == T_ACTOR || next == T_link {
				f.list = append(f.list, menu("addALink"))
			} else {
				f.list = append(f.list, signal(actor()))
			}
			expect(T_NEWLINE)
		case T_links:
			f.list = append(f.list, menu("addLinks"))
			expect(T_NEWLINE)
		case T_properties:
			f.list = append(f.list, menu("addProperties"))
			expect(T_NEWLINE)
		case T_details:
			f.list = append(f.list, menu("addDetails"))
			expect(T_NEWLINE)
		case T_note:
			f.list = append(f.list, note())
			expect(T_NEWLINE)
		case T_autonumber:
			f.list = append(f.list, autonumber())
		case T_activate, T_deactivate:
			i++
			who := actor()
			expect(T_NEWLINE)
			if t == T_activate {
				f.list = append(f.list, &SeqStatement{Type: "activeStart", SignalType: LINETYPE_ACTIVE_START, Actor: who.Actor})
			} else {
				f.list = append(f.list, &SeqStatement{Type: "activeEnd", SignalType: LINETYPE_ACTIVE_END, Actor: who.Actor})
			}
		case T_title, T_legacy_title:
			// The token is "title", then ":" for the legacy form, then one space.
			s := texts[i][5:]
			if t == T_legacy_title {
				s = s[1:]
			}
			_, size := utf8.DecodeRuneInString(s)
			title := s[size:]
			i++
			db.SetDiagramTitle(title)
			f.list = append(f.list, title)
		case T_acc_title:
			i++
			value := js.Trim(expect(T_acc_title_value))
			db.SetAccTitle(value)
			f.list = append(f.list, value)
		case T_acc_descr:
			i++
			value := js.Trim(expect(T_acc_descr_value))
			db.SetAccDescription(value)
			f.list = append(f.list, value)
		case T_acc_descr_multiline_value:
			value := js.Trim(texts[i])
			i++
			db.SetAccDescription(value)
			f.list = append(f.list, value)
		case T_loop, T_rect, T_opt, T_break, T_alt, T_par, T_par_over, T_critical, T_box:
			i++
			text := expect(T_restOfLine)
			stack = append(stack, f)
			start := LINETYPE_PAR_START
			if t == T_par_over {
				start = LINETYPE_PAR_OVER_START
			}
			f = &frame{kind: BLOCK[t], text: text, startType: start, list: []SeqItem{[]SeqItem{}}}
		case T_else, T_and, T_option:
			want := K_Critical
			if t == T_else {
				want = K_Alt
			} else if t == T_and {
				want = K_Par
			}
			if f.kind != want {
				fail("'end'")
			}
			i++
			text := expect(T_restOfLine)
			f.sections = append(f.sections, sectionEnd{list: f.list, text: text})
			f.list = []SeqItem{}
		case T_end:
			if f.kind == K_Top {
				fail("'NEWLINE', 'EOF'")
			}
			i++
			done := closeFrame(f)
			f = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			f.list = append(f.list, done)
		case T_END:
			if f.kind != K_Top {
				fail("'end'")
			}
			db.Apply(f.list)
			return
		default:
			fail("'NEWLINE', 'ACTOR', 'participant', 'note', 'end'")
		}
	}
}
