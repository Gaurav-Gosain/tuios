// Ported from pele src/diagrams/railroad/abnf.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"strings"
	"sync"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

// `railroad-abnf-beta`: ABNF as in RFC 5234, with each rule closed by a semicolon.

const (
	AB_keyword = iota
	AB_equals
	AB_semicolon
	AB_slash
	AB_group
	AB_groupEnd
	AB_option
	AB_optionEnd
	AB_title
	AB_accTitle
	AB_accDescr
	AB_name
	AB_string
	AB_number
	AB_repeat
	AB_exact
)

const DIGITS = "0123456789"

func isLetterByte(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isDigitByte(c byte) bool  { return c >= '0' && c <= '9' }
func isHexByte(c byte) bool {
	return isDigitByte(c) || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')
}

// /[A-Za-z][A-Za-z0-9-]*/y
func matchRuleName(src string, p int) int {
	if !isLetterByte(src[p]) {
		return -1
	}
	q := p + 1
	for q < len(src) && (isLetterByte(src[q]) || isDigitByte(src[q]) || src[q] == '-') {
		q++
	}
	return q
}

// /;[^\n\r]*/y
func matchAbnfComment(src string, p int) int {
	if src[p] != ';' {
		return -1
	}
	q := p + 1
	for q < len(src) && src[q] != '\n' && src[q] != '\r' {
		q++
	}
	return q
}

// /"[^"]*"/y
func matchAbnfString(src string, p int) int {
	if src[p] != '"' {
		return -1
	}
	if at := strings.IndexByte(src[p+1:], '"'); at >= 0 {
		return p + 1 + at + 1
	}
	return -1
}

func hexRun(src string, q int) int {
	for q < len(src) && isHexByte(src[q]) {
		q++
	}
	return q
}

// /%[xXdDbB][0-9A-Fa-f]+(?:-[0-9A-Fa-f]+|\.[0-9A-Fa-f]+)*/y
func matchNumVal(src string, p int) int {
	if src[p] != '%' || p+1 >= len(src) || strings.IndexByte("xXdDbB", src[p+1]) < 0 {
		return -1
	}
	q := hexRun(src, p+2)
	if q == p+2 {
		return -1
	}
	for q < len(src) && (src[q] == '-' || src[q] == '.') {
		e := hexRun(src, q+1)
		if e == q+1 {
			break
		}
		q = e
	}
	return q
}

// /[0-9]*\*[0-9]*/y
func matchRepeat(src string, p int) int {
	q := p
	for q < len(src) && isDigitByte(src[q]) {
		q++
	}
	if q >= len(src) || src[q] != '*' {
		return -1
	}
	q++
	for q < len(src) && isDigitByte(src[q]) {
		q++
	}
	return q
}

// /[0-9]+/y
func matchDigits(src string, p int) int {
	q := p
	for q < len(src) && isDigitByte(src[q]) {
		q++
	}
	if q == p {
		return -1
	}
	return q
}

var (
	abnfOnce   sync.Once
	abnfTokens []*common.TokenType
)

// ABNF_TOKENS is the token table, made on the first parse.
func ABNF_TOKENS() []*common.TokenType {
	abnfOnce.Do(func() {
		name := &common.TokenType{Name: "ABNF_RULENAME", Match: matchRuleName, First: LETTERS}
		comment := &common.TokenType{Name: "ABNF_COMMENT", Match: matchAbnfComment, Hidden: true, First: ";"}
		abnfTokens = []*common.TokenType{
			opening("railroad-abnf-beta", name),
			literal("="),
			// A semicolon closes a rule only at the end of its line; with anything after it, it starts a comment.
			literal(";", comment),
			literal("/"),
			literal("("),
			literal(")"),
			literal("["),
			literal("]"),
			title(titleAbnfRest),
			ACC_TITLE,
			ACC_DESCR,
			name,
			{Name: "ABNF_STRING", Match: matchAbnfString, First: "\""},
			{Name: "ABNF_NUMVAL", Match: matchNumVal, First: "%"},
			{Name: "ABNF_REPEAT", Match: matchRepeat, First: DIGITS + "*"},
			{Name: "ABNF_EXACT_REPEAT", Match: matchDigits, First: DIGITS},
		}
		abnfTokens = append(abnfTokens, hiddenTokens("ABNF")...)
		abnfTokens = append(abnfTokens, comment)
	})
	return abnfTokens
}

// abnfFrame is one open bracket, or the rule itself at the bottom of the stack.
type abnfFrame struct {
	alternation   *astNode
	concatenation *astNode
	// The bracket's node, its closing token, and the repeat written before it.
	node      *astNode
	close     int
	repeat    string
	hasRepeat bool
}

func newAbnfFrame(node, alternation *astNode, close int, repeat string, hasRepeat bool) *abnfFrame {
	concatenation := &astNode{typ: "AbnfConcatenation"}
	alternation.alternatives = append(alternation.alternatives, concatenation)
	return &abnfFrame{alternation: alternation, concatenation: concatenation, node: node, close: close, repeat: repeat, hasRepeat: hasRepeat}
}

func abnfAlternation(r *common.Reader) *astNode {
	tokens := ABNF_TOKENS()
	var stack []*abnfFrame
	top := newAbnfFrame(nil, &astNode{typ: "AbnfAlternation"}, -2, "", false)
	for {
		repeat, hasRepeat := "", false
		if r.Kind() == AB_repeat || r.Kind() == AB_exact {
			repeat, hasRepeat = r.Take(), true
		}
		var primary *astNode
		kind := r.Kind()
		switch kind {
		case AB_string:
			text := r.Take()
			primary = &astNode{typ: "AbnfStringLiteral", value: text[1 : len(text)-1]}
		case AB_number:
			primary = &astNode{typ: "AbnfNumVal", value: r.Take()}
		case AB_name:
			primary = &astNode{typ: "AbnfRuleName", name: r.Take()}
		case AB_group, AB_option:
			r.I++
			element := &astNode{typ: "AbnfAlternation"}
			stack = append(stack, top)
			typ := "AbnfOptionalGroup"
			if kind == AB_group {
				typ = "AbnfGroup"
			}
			top = newAbnfFrame(&astNode{typ: typ, element: element}, element, kind+1, repeat, hasRepeat)
			continue
		default:
			r.Fail("a string, a number, a name, or an opening bracket")
		}
		for {
			top.concatenation.elements = append(top.concatenation.elements, &astNode{typ: "AbnfElement", repeat: repeat, hasRepeat: hasRepeat, primary: primary})
			k := r.Kind()
			if k == top.close {
				r.I++
				primary = top.node
				repeat, hasRepeat = top.repeat, top.hasRepeat
				top = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				continue
			}
			if k == AB_slash {
				r.I++
				top.concatenation = &astNode{typ: "AbnfConcatenation"}
				top.alternation.alternatives = append(top.alternation.alternatives, top.concatenation)
			} else if k != AB_string && k != AB_number && k != AB_name && k != AB_group && k != AB_option && k != AB_repeat && k != AB_exact {
				if len(stack) == 0 {
					return top.alternation
				}
				r.Fail("token of type '" + tokens[top.close].Name + "'")
			}
			break
		}
	}
}

func parseAbnf(src string) *Grammar {
	return parseGrammar(src, ABNF_TOKENS(), AB_title, abnfTitle, func(r *common.Reader) *astRule {
		name := r.Expect(AB_name)
		r.Expect(AB_equals)
		definition := abnfAlternation(r)
		r.Expect(AB_semicolon)
		return &astRule{name: name, definition: definition}
	})
}

func abnfNode(part *astNode, kids []*RailroadNode) *RailroadNode {
	switch part.typ {
	case "AbnfAlternation":
		return choice(kids)
	case "AbnfConcatenation":
		return sequence(kids)
	case "AbnfElement":
		repeat := part.repeat
		if repeat == "" {
			return kids[0]
		}
		star := strings.IndexByte(repeat, '*')
		exact := star == -1
		min := 0.0
		if star > 0 || exact {
			min = js.ParseIntPrefix(repeat, 10)
		}
		max := inf
		if exact {
			max = min
		} else if star < len(repeat)-1 {
			max = js.ParseIntPrefix(repeat[star+1:], 10)
		}
		if min == 0 && max == 1 {
			return optional(kids[0])
		}
		return repetition(kids[0], min, max)
	case "AbnfStringLiteral", "AbnfNumVal":
		return terminal(part.value)
	case "AbnfRuleName":
		return nonterminal(part.name)
	case "AbnfGroup":
		return kids[0]
	}
	return optional(kids[0])
}

func abnfRules(ast *Grammar) []RailroadRule { return toRules(ast, abnfNode) }

// abnfTitle is text.slice(1, -1): a lone quote, as in `title "`, gives "".
func abnfTitle(text string) string {
	if len(text) < 2 {
		return ""
	}
	return text[1 : len(text)-1]
}
