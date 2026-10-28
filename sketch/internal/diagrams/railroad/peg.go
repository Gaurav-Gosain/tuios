// Ported from pele src/diagrams/railroad/peg.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"sync"

	"tuios.dev/sketch/internal/diagrams/common"
)

// `railroad-peg-beta`: parsing expression grammars.

const (
	PG_keyword = iota
	PG_arrow
	PG_semicolon
	PG_slash
	PG_and
	PG_not
	PG_question
	PG_star
	PG_plus
	PG_group
	PG_groupEnd
	PG_dot
	PG_title
	PG_accTitle
	PG_accDescr
	PG_id
	PG_string
)

// /#[^\n\r]*/y
func matchHashComment(src string, p int) int {
	if src[p] != '#' {
		return -1
	}
	q := p + 1
	for q < len(src) && src[q] != '\n' && src[q] != '\r' {
		q++
	}
	return q
}

var (
	pegOnce   sync.Once
	pegTokens []*common.TokenType
)

// PEG_TOKENS is the token table, made on the first parse.
func PEG_TOKENS() []*common.TokenType {
	pegOnce.Do(func() {
		id := identifier("PEG_ID")
		pegTokens = []*common.TokenType{
			opening("railroad-peg-beta", id),
			literal("<-"),
			literal(";"),
			literal("/"),
			literal("&"),
			literal("!"),
			literal("?"),
			literal("*"),
			literal("+"),
			literal("("),
			literal(")"),
			literal("."),
			title(titleWordRest),
			ACC_TITLE,
			ACC_DESCR,
			id,
			quoted("PEG_STRING"),
		}
		pegTokens = append(pegTokens, hiddenTokens("PEG")...)
		pegTokens = append(pegTokens, &common.TokenType{Name: "PEG_LINE_COMMENT", Match: matchHashComment, Hidden: true, First: "#"})
	})
	return pegTokens
}

// pegFrame is one open group, or the rule itself at the bottom of the stack.
type pegFrame struct {
	choice   *astNode
	sequence *astNode
	// The group's node and the predicate written before it.
	node        *astNode
	operator    string
	hasOperator bool
}

func newPegFrame(node, choice *astNode, operator string, hasOperator bool) *pegFrame {
	sequence := &astNode{typ: "PegSequence"}
	choice.alternatives = append(choice.alternatives, sequence)
	return &pegFrame{choice: choice, sequence: sequence, node: node, operator: operator, hasOperator: hasOperator}
}

func pegDefinition(r *common.Reader) *astNode {
	var stack []*pegFrame
	top := newPegFrame(nil, &astNode{typ: "PegOrderedChoice"}, "", false)
	for {
		operator, hasOperator := "", false
		if r.Kind() == PG_and || r.Kind() == PG_not {
			operator, hasOperator = r.Take(), true
		}
		var primary *astNode
		kind := r.Kind()
		switch kind {
		case PG_string:
			primary = &astNode{typ: "PegLiteral", value: unescape(r.Take())}
		case PG_id:
			primary = &astNode{typ: "PegIdentifier", name: r.Take()}
		case PG_dot:
			primary = &astNode{typ: "PegAny", dot: r.Take()}
		case PG_group:
			r.I++
			element := &astNode{typ: "PegOrderedChoice"}
			stack = append(stack, top)
			top = newPegFrame(&astNode{typ: "PegGroup", element: element}, element, operator, hasOperator)
			continue
		default:
			r.Fail("a literal, a name, a dot, or an opening parenthesis")
		}
		for {
			suffix := &astNode{typ: "PegSuffix", primary: primary}
			if r.Kind() == PG_question || r.Kind() == PG_star || r.Kind() == PG_plus {
				suffix.operator, suffix.hasOperator = r.Take(), true
			}
			top.sequence.elements = append(top.sequence.elements, &astNode{typ: "PegPrefix", operator: operator, hasOperator: hasOperator, suffix: suffix})
			k := r.Kind()
			if k == PG_groupEnd && len(stack) > 0 {
				r.I++
				primary = top.node
				operator, hasOperator = top.operator, top.hasOperator
				top = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				continue
			}
			if k == PG_slash {
				r.I++
				top.sequence = &astNode{typ: "PegSequence"}
				top.choice.alternatives = append(top.choice.alternatives, top.sequence)
			} else if k != PG_and && k != PG_not && k != PG_string && k != PG_id && k != PG_dot && k != PG_group {
				if len(stack) == 0 {
					return top.choice
				}
				r.Fail("token of type ')'")
			}
			break
		}
	}
}

func parsePeg(src string) *Grammar {
	return parseGrammar(src, PEG_TOKENS(), PG_title, unescape, func(r *common.Reader) *astRule {
		name := r.Expect(PG_id)
		r.Expect(PG_arrow)
		rule := &astRule{name: name, definition: pegDefinition(r)}
		r.Expect(PG_semicolon)
		return rule
	})
}

// pegLabel is what a predicate shows of the expression it looks ahead for.
func pegLabel(node *RailroadNode) string {
	switch node.typ {
	case "terminal":
		return "\"" + node.value + "\""
	case "nonterminal":
		return node.name
	case "special":
		return node.text
	}
	return "(...)"
}

func pegNode(part *astNode, kids []*RailroadNode) *RailroadNode {
	var kid *RailroadNode
	if len(kids) > 0 {
		kid = kids[0]
	}
	switch part.typ {
	case "PegOrderedChoice":
		return choice(kids)
	case "PegSequence":
		return sequence(kids)
	case "PegPrefix":
		if part.operator != "" {
			return special(part.operator + pegLabel(kid))
		}
		return kid
	case "PegSuffix":
		if part.operator == "" {
			return kid
		}
		if part.operator == "?" {
			return optional(kid)
		}
		min := 0.0
		if part.operator == "+" {
			min = 1
		}
		return repetition(kid, min, inf)
	case "PegLiteral":
		return terminal(part.value)
	case "PegIdentifier":
		return nonterminal(part.name)
	case "PegAny":
		return special(part.dot)
	}
	return kid
}

func pegRules(ast *Grammar) []RailroadRule { return toRules(ast, pegNode) }
