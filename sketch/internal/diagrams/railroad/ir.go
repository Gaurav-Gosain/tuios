// Ported from pele src/diagrams/railroad/ir.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"sync"

	"tuios.dev/sketch/internal/diagrams/common"
)

// `railroad-beta`: Mermaid's own notation, one constructor call per node.

const (
	IR_keyword = iota
	IR_nonterminal
	IR_zeroOrMore
	IR_oneOrMore
	IR_sequence
	IR_optional
	IR_terminal
	IR_special
	IR_choice
	IR_equals
	IR_semicolon
	IR_open
	IR_comma
	IR_close
	IR_title
	IR_accTitle
	IR_accDescr
	IR_id
	IR_string
)

var (
	irOnce   sync.Once
	irTokens []*common.TokenType
)

// IR_TOKENS is the token table, made on the first parse.
func IR_TOKENS() []*common.TokenType {
	irOnce.Do(func() {
		id := identifier("RR_ID")
		word := func(name string) *common.TokenType { return literal(name, id) }
		comment, open := blockComment("/*", "*/")
		irTokens = append([]*common.TokenType{
			opening("railroad-beta", id),
			word("nonterminal"),
			word("zeroOrMore"),
			word("oneOrMore"),
			word("sequence"),
			word("optional"),
			word("terminal"),
			word("special"),
			word("choice"),
			literal("="),
			literal(";"),
			literal("("),
			literal(","),
			literal(")"),
			title(titleWordRest),
			ACC_TITLE,
			ACC_DESCR,
			id,
			quoted("RR_STRING"),
		}, hiddenTokens("RR")...)
		irTokens = append(irTokens, &common.TokenType{Name: "RR_BLOCK_COMMENT", Match: comment, Hidden: true, First: "/", Opener: open})
	})
	return irTokens
}

func argument(r *common.Reader) string {
	r.I++
	r.Expect(IR_open)
	value := unescape(r.Expect(IR_string))
	r.Expect(IR_close)
	return value
}

func expression(r *common.Reader) *astNode {
	var open []*astNode
outer:
	for {
		var value *astNode
		switch r.Kind() {
		case IR_terminal:
			value = &astNode{typ: "RailroadTerminalExpr", value: argument(r)}
		case IR_nonterminal:
			value = &astNode{typ: "RailroadNonTerminalExpr", name: argument(r)}
		case IR_special:
			value = &astNode{typ: "RailroadSpecialExpr", text: argument(r)}
		case IR_sequence:
			value = &astNode{typ: "RailroadSequenceExpr"}
		case IR_choice:
			value = &astNode{typ: "RailroadChoiceExpr"}
		case IR_optional:
			value = &astNode{typ: "RailroadOptionalExpr"}
		case IR_oneOrMore:
			value = &astNode{typ: "RailroadOneOrMoreExpr"}
		case IR_zeroOrMore:
			value = &astNode{typ: "RailroadZeroOrMoreExpr"}
		default:
			r.Fail("an expression")
		}
		switch value.typ {
		case "RailroadSequenceExpr", "RailroadChoiceExpr", "RailroadOptionalExpr", "RailroadOneOrMoreExpr", "RailroadZeroOrMoreExpr":
			r.I++
			r.Expect(IR_open)
			open = append(open, value)
			continue
		}
		for {
			if len(open) == 0 {
				return value
			}
			parent := open[len(open)-1]
			switch parent.typ {
			case "RailroadSequenceExpr", "RailroadChoiceExpr":
				if parent.typ == "RailroadSequenceExpr" {
					parent.elements = append(parent.elements, value)
				} else {
					parent.alternatives = append(parent.alternatives, value)
				}
				if r.Accept(IR_comma) {
					continue outer
				}
			default:
				parent.element = value
			}
			r.Expect(IR_close)
			open = open[:len(open)-1]
			value = parent
		}
	}
}

func parseIr(src string) *Grammar {
	return parseGrammar(src, IR_TOKENS(), IR_title, unescape, func(r *common.Reader) *astRule {
		name := r.Expect(IR_id)
		r.Expect(IR_equals)
		definition := expression(r)
		r.Expect(IR_semicolon)
		return &astRule{name: name, definition: definition}
	})
}

func irNode(e *astNode, kids []*RailroadNode) *RailroadNode {
	switch e.typ {
	case "RailroadTerminalExpr":
		return terminal(e.value)
	case "RailroadNonTerminalExpr":
		return nonterminal(e.name)
	case "RailroadSpecialExpr":
		return special(e.text)
	case "RailroadSequenceExpr":
		return sequence(kids)
	case "RailroadChoiceExpr":
		return choice(kids)
	case "RailroadOptionalExpr":
		return optional(kids[0])
	}
	min := 0.0
	if e.typ == "RailroadOneOrMoreExpr" {
		min = 1
	}
	return repetition(kids[0], min, inf)
}

func irRules(ast *Grammar) []RailroadRule { return toRules(ast, irNode) }
