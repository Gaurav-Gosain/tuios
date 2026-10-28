// Ported from pele src/diagrams/railroad/ebnf.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"sync"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

// `railroad-ebnf-beta`: EBNF in the W3C and ISO 14977 styles.

const (
	EB_keyword = iota
	EB_defines
	EB_equals
	EB_semicolon
	EB_bar
	EB_comma
	EB_group
	EB_groupEnd
	EB_option
	EB_optionEnd
	EB_repeat
	EB_repeatEnd
	EB_question
	EB_star
	EB_plus
	EB_minus
	EB_title
	EB_accTitle
	EB_accDescr
	EB_id
	EB_string
	EB_special
)

// specialSequence is a question mark, text with something other than blanks in it and no `?` or `;`, and a question mark.
// Mermaid's pattern looks ahead with two nested runs, which rescans; this reads the text once.
func specialSequence(src string, at int) int {
	if src[at] != 63 {
		return -1
	}
	solid := false
	for i := at + 1; i < len(src); {
		c, size := utf8.DecodeRuneInString(src[i:])
		if c == 63 {
			if solid {
				return i + 1
			}
			return -1
		}
		if c == 59 {
			return -1
		}
		solid = solid || !isSpace(c)
		i += size
	}
	return -1
}

var (
	ebnfOnce   sync.Once
	ebnfTokens []*common.TokenType
)

// EBNF_TOKENS is the token table, made on the first parse.
func EBNF_TOKENS() []*common.TokenType {
	ebnfOnce.Do(func() {
		id := identifier("EBNF_ID")
		special := &common.TokenType{Name: "EBNF_SPECIAL_SEQUENCE", First: "?", Scanner: func() common.Scanner { return specialSequence }}
		hidden := hiddenTokens("EBNF")
		isoMatch, isoOpen := blockComment("(*", "*)")
		iso := &common.TokenType{Name: "EBNF_ISO_COMMENT", Match: isoMatch, Hidden: true, First: "(", Opener: isoOpen}
		blockMatch, blockOpen := blockComment("/*", "*/")
		ebnfTokens = []*common.TokenType{
			opening("railroad-ebnf-beta", id),
			literal("::="),
			literal("="),
			literal(";"),
			literal("|"),
			literal(","),
			literal("(", iso),
			literal(")"),
			literal("["),
			literal("]"),
			literal("{"),
			literal("}"),
			literal("?", special),
			literal("*"),
			literal("+"),
			literal("-", hidden[1]),
			title(titleWordRest),
			ACC_TITLE,
			ACC_DESCR,
			id,
			quoted("EBNF_STRING"),
			special,
		}
		ebnfTokens = append(ebnfTokens, hidden...)
		ebnfTokens = append(ebnfTokens,
			&common.TokenType{Name: "EBNF_BLOCK_COMMENT", Match: blockMatch, Hidden: true, First: "/", Opener: blockOpen},
			iso)
	})
	return ebnfTokens
}

// ebnfFrame is one open bracket, or the rule itself at the bottom of the stack.
type ebnfFrame struct {
	choice   *astNode
	sequence *astNode
	term     *astNode
	// The bracket's node, its closing token, and whether it is the right side of an exception.
	node   *astNode
	close  int
	except bool
}

func newEbnfFrame(node, choice *astNode, close int, except bool) *ebnfFrame {
	sequence := &astNode{typ: "EbnfSequence"}
	choice.alternatives = append(choice.alternatives, sequence)
	return &ebnfFrame{choice: choice, sequence: sequence, node: node, close: close, except: except}
}

var BRACKETS = [...]string{"EbnfGroup", "EbnfOptional", "EbnfRepetition"}

func ebnfDefinition(r *common.Reader) *astNode {
	tokens := EBNF_TOKENS()
	var stack []*ebnfFrame
	top := newEbnfFrame(nil, &astNode{typ: "EbnfChoice"}, -2, false)
	except := false
	for {
		var primary *astNode
		kind := r.Kind()
		switch kind {
		case EB_string:
			primary = &astNode{typ: "EbnfTerminal", value: unescape(r.Take())}
		case EB_id:
			primary = &astNode{typ: "EbnfNonTerminal", name: r.Take()}
		case EB_special:
			text := r.Take()
			primary = &astNode{typ: "EbnfSpecial", text: js.Trim(text[1 : len(text)-1])}
		case EB_group, EB_option, EB_repeat:
			r.I++
			element := &astNode{typ: "EbnfChoice"}
			stack = append(stack, top)
			top = newEbnfFrame(&astNode{typ: BRACKETS[(kind-EB_group)>>1], element: element}, element, kind+1, except)
			except = false
			continue
		default:
			r.Fail("a terminal, a name, or an opening bracket")
		}
		for {
			if except {
				top.term.postfixes = append(top.term.postfixes, &astNode{typ: "EbnfExceptionPostfix", except: primary})
				except = false
			} else {
				top.term = &astNode{typ: "EbnfTerm", base: primary}
				top.sequence.elements = append(top.sequence.elements, top.term)
			}
			for {
				k := r.Kind()
				if k == EB_question {
					top.term.postfixes = append(top.term.postfixes, &astNode{typ: "EbnfOptionalPostfix", operator: r.Take()})
				} else if k == EB_star {
					top.term.postfixes = append(top.term.postfixes, &astNode{typ: "EbnfZeroOrMorePostfix", operator: r.Take()})
				} else if k == EB_plus {
					top.term.postfixes = append(top.term.postfixes, &astNode{typ: "EbnfOneOrMorePostfix", operator: r.Take()})
				} else {
					break
				}
			}
			k := r.Kind()
			if k == top.close {
				r.I++
				primary = top.node
				except = top.except
				top = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				continue
			}
			if k == EB_minus {
				r.I++
				except = true
			} else if k == EB_comma {
				r.I++
			} else if k == EB_bar {
				r.I++
				top.sequence = &astNode{typ: "EbnfSequence"}
				top.choice.alternatives = append(top.choice.alternatives, top.sequence)
			} else if k != EB_string && k != EB_id && k != EB_special && k != EB_group && k != EB_option && k != EB_repeat {
				if len(stack) == 0 {
					return top.choice
				}
				r.Fail("token of type '" + tokens[top.close].Name + "'")
			}
			break
		}
	}
}

func parseEbnf(src string) *Grammar {
	return parseGrammar(src, EBNF_TOKENS(), EB_title, unescape, func(r *common.Reader) *astRule {
		name := r.Expect(EB_id)
		if !r.Accept(EB_equals) {
			r.Expect(EB_defines)
		}
		rule := &astRule{name: name, definition: ebnfDefinition(r)}
		r.Expect(EB_semicolon)
		return rule
	})
}

func ebnfNode(part *astNode, kids []*RailroadNode) *RailroadNode {
	switch part.typ {
	case "EbnfChoice":
		return choice(kids)
	case "EbnfSequence":
		return sequence(kids)
	case "EbnfTerm":
		// Each postfix operator applies to everything before it in the term.
		result := kids[0]
		next := 1
		for _, postfix := range part.postfixes {
			switch postfix.typ {
			case "EbnfOptionalPostfix":
				result = optional(result)
			case "EbnfExceptionPostfix":
				result = &RailroadNode{typ: "sequence", elements: []*RailroadNode{result, terminal("-"), kids[next]}}
				next++
			default:
				min := 0.0
				if postfix.typ == "EbnfOneOrMorePostfix" {
					min = 1
				}
				result = repetition(result, min, inf)
			}
		}
		return result
	case "EbnfTerminal":
		return terminal(part.value)
	case "EbnfNonTerminal":
		return nonterminal(part.name)
	case "EbnfSpecial":
		return special(part.text)
	case "EbnfGroup":
		return kids[0]
	case "EbnfOptional":
		return optional(kids[0])
	}
	return repetition(kids[0], 0, inf)
}

func ebnfRules(ast *Grammar) []RailroadRule { return toRules(ast, ebnfNode) }
