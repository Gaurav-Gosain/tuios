// Ported from pele src/diagrams/requirement/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package requirement

import (
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// VALUES: what the keyword tokens stand for, from REQUIREMENT to TRACES in token order.
var VALUES = [...]string{
	"Requirement",
	"Functional Requirement",
	"Interface Requirement",
	"Performance Requirement",
	"Physical Requirement",
	"Design Constraint",
	"Low",
	"Medium",
	"High",
	"Analysis",
	"Demonstration",
	"Inspection",
	"Test",
	"",
	"contains",
	"copies",
	"derives",
	"satisfies",
	"verifies",
	"refines",
	"traces",
}

func isStylePart(t int) bool {
	return t == T_ALPHA || t == T_COLON || t == T_BRKT || t == T_MINUS || t == T_SEMICOLON
}

func parseRequirement(src string, db *RequirementDb) {
	toks := tokenize(src)
	typs, texts, starts := toks.types, toks.texts, toks.starts
	i := 0
	var fields []int
	var values []string

	// fail fails with Jison's wording, naming the token types `from` to `to` and any others as expected.
	fail := func(from, to int, others ...int) {
		t := typs[i]
		if t == T_ERROR {
			panic(types.SyntaxError("requirement", src, starts[i], "", true))
		}
		var b strings.Builder
		b.WriteString("Expecting ")
		first := true
		name := func(typ int) {
			if !first {
				b.WriteString(", ")
			}
			first = false
			b.WriteString("'" + TOKEN_NAMES[typ] + "'")
		}
		for k := from; k <= to; k++ {
			name(k)
		}
		for _, k := range others {
			name(k)
		}
		b.WriteString(", got '" + TOKEN_NAMES[t] + "'")
		panic(types.SyntaxError("requirement", src, starts[i], b.String(), false))
	}

	expect := func(typ int) string {
		if typs[i] != typ {
			fail(typ, typ)
		}
		i++
		return texts[i-1]
	}

	// keyword is the value of the next token if it is a keyword in the given range of token types.
	keyword := func(from, to int) string {
		t := typs[i]
		if t < from || t > to {
			fail(from, to)
		}
		i++
		return VALUES[t-T_REQUIREMENT]
	}

	str := func() string {
		t := typs[i]
		if t != T_unqString && t != T_qString {
			fail(T_qString, T_unqString)
		}
		i++
		return texts[i-1]
	}

	directive := func() bool {
		switch typs[i] {
		case T_acc_title:
			i++
			db.setAccTitle(js.Trim(expect(T_acc_title_value)))
		case T_acc_descr:
			i++
			db.setAccDescription(js.Trim(expect(T_acc_descr_value)))
		case T_acc_descr_multiline_value:
			db.setAccDescription(js.Trim(texts[i]))
			i++
		default:
			return false
		}
		return true
	}

	idList := func() []string {
		var list []string
		for {
			t := typs[i]
			if t != T_ALPHA && t != T_unqString && t != T_qString {
				fail(T_qString, T_unqString, T_ALPHA)
			}
			list = append(list, texts[i])
			i++
			if typs[i] != T_COMMA {
				return list
			}
			i++
		}
	}

	stylesOpt := func() []string {
		var styles []string
		for {
			if !isStylePart(typs[i]) {
				fail(T_ALPHA, T_SEMICOLON, T_MINUS, T_BRKT)
			}
			style := texts[i]
			i++
			if isStylePart(typs[i]) {
				// A builder keeps a long run linear; pele's += is linear only through V8 ropes.
				var b strings.Builder
				b.WriteString(style)
				for isStylePart(typs[i]) {
					b.WriteString(texts[i])
					i++
				}
				style = b.String()
			}
			styles = append(styles, style)
			if typs[i] != T_COMMA {
				return styles
			}
			i++
		}
	}

	classList := func() []string {
		var classes []string
		switch typs[i] {
		case T_STYLE_SEPARATOR:
			i++
			classes = idList()
			expect(T_STRUCT_START)
		case T_STRUCT_START:
			i++
		default:
			fail(T_STRUCT_START, T_STRUCT_START, T_STYLE_SEPARATOR)
		}
		expect(T_NEWLINE)
		return classes
	}

	// body reads a body up to its closing brace. The grammar is right recursive, so Mermaid applies
	// the fields last to first and the first of two repeated fields wins.
	body := func(requirement bool) {
		fields = fields[:0]
		values = values[:0]
		for {
			t := typs[i]
			if t == T_STRUCT_STOP {
				i++
				break
			}
			if t == T_NEWLINE {
				i++
				continue
			}
			if requirement && (t < T_ID || t > T_VERIFYMTHD) {
				fail(T_ID, T_VERIFYMTHD, T_NEWLINE, T_STRUCT_STOP)
			}
			if !requirement && t != T_TYPE && t != T_DOCREF {
				fail(T_TYPE, T_DOCREF, T_NEWLINE, T_STRUCT_STOP)
			}
			i++
			expect(T_COLONSEP)
			switch t {
			case T_RISK:
				values = append(values, keyword(T_LOW_RISK, T_HIGH_RISK))
			case T_VERIFYMTHD:
				values = append(values, keyword(T_VERIFY_ANALYSIS, T_VERIFY_TEST))
			default:
				values = append(values, str())
			}
			fields = append(fields, t)
			expect(T_NEWLINE)
		}
		for k := len(fields) - 1; k >= 0; k-- {
			value := values[k]
			switch fields[k] {
			case T_ID:
				db.setNewReqId(value)
			case T_TEXT:
				db.setNewReqText(value)
			case T_RISK:
				db.setNewReqRisk(value)
			case T_VERIFYMTHD:
				db.setNewReqVerifyMethod(value)
			case T_TYPE:
				db.setNewElementType(value)
			default:
				db.setNewElementDocRef(value)
			}
		}
	}

	for directive() {
		if typs[i] == T_NEWLINE {
			i++
		}
	}
	if typs[i] != T_RD {
		fail(T_RD, T_RD, T_acc_title, T_acc_descr, T_acc_descr_multiline_value)
	}
	i++
	expect(T_NEWLINE)

	for {
		t := typs[i]
		switch {
		case t == T_EOF:
			return
		case t == T_NEWLINE:
			i++
		case directive():
		case t >= T_direction_tb && t <= T_direction_lr:
			i++
			db.setDirection([...]string{"TB", "BT", "RL", "LR"}[t-T_direction_tb])
		case t >= T_REQUIREMENT && t <= T_DESIGN_CONSTRAINT:
			typ := keyword(T_REQUIREMENT, T_DESIGN_CONSTRAINT)
			name := str()
			classes := classList()
			body(true)
			db.addRequirement(name, typ)
			if classes != nil {
				db.setClass([]string{name}, classes)
			}
		case t == T_ELEMENT:
			i++
			name := str()
			classes := classList()
			body(false)
			db.addElement(name)
			if classes != nil {
				db.setClass([]string{name}, classes)
			}
		case t == T_STYLE:
			i++
			ids := idList()
			db.setCssStyle(ids, stylesOpt())
		case t == T_CLASSDEF:
			i++
			ids := idList()
			db.defineClass(ids, stylesOpt())
		case t == T_CLASS:
			i++
			ids := idList()
			db.setClass(ids, idList())
		case t == T_unqString || t == T_qString:
			id := texts[i]
			i++
			next := typs[i]
			switch next {
			case T_STYLE_SEPARATOR:
				i++
				db.setClass([]string{id}, idList())
			case T_END_ARROW_L, T_LINE:
				i++
				relationship := keyword(T_CONTAINS, T_TRACES)
				if next == T_LINE {
					expect(T_END_ARROW_R)
				} else {
					expect(T_LINE)
				}
				other := str()
				if next == T_LINE {
					db.addRelationship(relationship, id, other)
				} else {
					db.addRelationship(relationship, other, id)
				}
			default:
				fail(T_STYLE_SEPARATOR, T_STYLE_SEPARATOR, T_END_ARROW_L, T_LINE)
			}
		default:
			fail(T_NEWLINE, T_EOF, T_acc_title, T_acc_descr, T_acc_descr_multiline_value, T_direction_tb, T_direction_bt,
				T_direction_rl, T_direction_lr, T_REQUIREMENT, T_FUNCTIONAL_REQUIREMENT, T_INTERFACE_REQUIREMENT,
				T_PERFORMANCE_REQUIREMENT, T_PHYSICAL_REQUIREMENT, T_DESIGN_CONSTRAINT, T_ELEMENT, T_CLASSDEF, T_CLASS,
				T_STYLE, T_unqString, T_qString)
		}
	}
}
