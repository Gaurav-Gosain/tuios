// Ported from pele src/diagrams/railroad/common.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

// What the four railroad grammars share: the title and accessibility tokens (which, unlike
// the ones in Mermaid's common grammar, do not take leading blanks), the hidden tokens, the
// statements before the first rule, string values, and the walk from a syntax tree to the rule model.

func isSpace(c rune) bool {
	return c == 32 || (c >= 9 && c <= 13) || (c > 127 && js.IsSpace(c))
}

// spaceAt reports whether src holds a white space character at i, and its length.
func spaceAt(src string, i int) (bool, int) {
	if i >= len(src) {
		return false, 0
	}
	c, size := utf8.DecodeRuneInString(src[i:])
	return isSpace(c), size
}

// accDescrScanner: the braced form scans to the next closing brace. Once none is found, none exists further on
// either, so later braced descriptions fail without scanning again.
func accDescrScanner() common.Scanner {
	unclosed := -1
	return func(src string, at int) int {
		if !strings.HasPrefix(src[at:], "accDescr") {
			return -1
		}
		n := len(src)
		i := at + 8
		for i < n && (src[i] == 32 || src[i] == 9) {
			i++
		}
		if i < n && src[i] == 58 {
			for i++; i < n; i++ {
				c := src[i]
				if c == 10 || c == 13 || (c == 37 && i+1 < n && src[i+1] == 37) {
					break
				}
			}
			return i
		}
		for {
			ok, size := spaceAt(src, i)
			if !ok {
				break
			}
			i += size
		}
		if i >= n || src[i] != 123 || (unclosed >= 0 && i >= unclosed) {
			return -1
		}
		close := strings.IndexByte(src[i+1:], '}')
		if close == -1 {
			unclosed = i
			return -1
		}
		return i + 1 + close + 1
	}
}

const LETTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// toCommentOrLineEnd is [^\n\r]*?(?=%%)|[^\n\r]* from q.
func toCommentOrLineEnd(src string, q int) int {
	end := q
	for end < len(src) && src[end] != '\n' && src[end] != '\r' {
		end++
	}
	if at := strings.Index(src[q:end], "%%"); at >= 0 {
		return q + at
	}
	return end
}

// title is /title(?:[\t ][^\n\r]*?(?=%%)|[\t ][^\n\r]*|(?!rest))/y: `rest` reports whether a name
// suffix starts at q, so `title` does not split longer names.
func title(rest func(c byte) bool) *common.TokenType {
	return &common.TokenType{Name: "TITLE", First: "t", Match: func(src string, p int) int {
		if !strings.HasPrefix(src[p:], "title") {
			return -1
		}
		q := p + 5
		if q < len(src) && (src[q] == ' ' || src[q] == '\t') {
			return toCommentOrLineEnd(src, q+1)
		}
		if q < len(src) && rest(src[q]) {
			return -1
		}
		return q
	}}
}

// titleWordRest is [\w-] and titleAbnfRest [A-Za-z0-9-].
func titleWordRest(c byte) bool { return common.IsWord(int(c)) || c == '-' }
func titleAbnfRest(c byte) bool { return c != '_' && titleWordRest(c) }

// /accTitle[\t ]*:(?:[^\n\r]*?(?=%%)|[^\n\r]*)/y
func matchAccTitle(src string, p int) int {
	if !strings.HasPrefix(src[p:], "accTitle") {
		return -1
	}
	q := p + 8
	for q < len(src) && (src[q] == ' ' || src[q] == '\t') {
		q++
	}
	if q >= len(src) || src[q] != ':' {
		return -1
	}
	return toCommentOrLineEnd(src, q+1)
}

var ACC_TITLE = &common.TokenType{Name: "ACC_TITLE", Match: matchAccTitle, First: "a"}
var ACC_DESCR = &common.TokenType{Name: "ACC_DESCR", Scanner: accDescrScanner, First: "a"}

func literal(name string, longer ...*common.TokenType) *common.TokenType {
	return &common.TokenType{Name: name, Pattern: name, Longer: longer}
}

// opening is the diagram's keyword. A name that starts with it and goes on is a name.
func opening(word string, name *common.TokenType) *common.TokenType {
	kw := *common.Keyword(word)
	kw.Longer = []*common.TokenType{name}
	return &kw
}

// /[A-Z_a-z][\w-]*/y
func matchIdentifier(src string, p int) int {
	c := src[p]
	if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return -1
	}
	q := p + 1
	for q < len(src) {
		c := src[q]
		if c == '-' || c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			q++
			continue
		}
		break
	}
	return q
}

func identifier(name string) *common.TokenType {
	return &common.TokenType{Name: name, Match: matchIdentifier, First: LETTERS + "_"}
}

func quoted(name string) *common.TokenType {
	t := *common.STRING
	t.Name = name
	return &t
}

// /[\t \r\n]+/y
func matchWhitespace(src string, p int) int {
	q := p
	for q < len(src) && (src[q] == '\t' || src[q] == ' ' || src[q] == '\r' || src[q] == '\n') {
		q++
	}
	if q == p {
		return -1
	}
	return q
}

// hiddenTokens are whitespace, front matter, directive, and `%%` comment, in the order every railroad grammar lists them.
func hiddenTokens(prefix string) []*common.TokenType {
	yaml, directive, comment := *common.YAML, *common.DIRECTIVE, *common.SINGLE_LINE_COMMENT
	yaml.Name, yaml.First = prefix+"_YAML", "-"
	directive.Name, directive.First = prefix+"_DIRECTIVE", "\t %"
	comment.Name, comment.First = prefix+"_SINGLE_LINE_COMMENT", "\t %"
	return []*common.TokenType{
		{Name: prefix + "_WHITESPACE", Match: matchWhitespace, Hidden: true, First: "\t \r\n"},
		&yaml,
		&directive,
		&comment,
	}
}

// blockComment matches open, any text, and close, the first close after open; openOnly
// matches open alone, for the Opener that marks an unclosed comment.
func blockComment(open, close string) (match, openOnly common.Scanner) {
	match = func(src string, p int) int {
		if !strings.HasPrefix(src[p:], open) {
			return -1
		}
		if at := strings.Index(src[p+len(open):], close); at >= 0 {
			return p + len(open) + at + len(close)
		}
		return -1
	}
	openOnly = func(src string, p int) int {
		if strings.HasPrefix(src[p:], open) {
			return p + len(open)
		}
		return -1
	}
	return match, openOnly
}

// unescape: Mermaid's railroad value converters know three escapes; any other escaped character stands for itself.
func unescape(text string) string {
	if len(text) < 2 {
		return ""
	}
	if !strings.Contains(text, "\\") {
		return text[1 : len(text)-1]
	}
	end := len(text) - 1
	var out strings.Builder
	for i := 1; i < end; {
		c, size := utf8.DecodeRuneInString(text[i:])
		if c == '\\' && i+1 < end {
			i++
			c, size = utf8.DecodeRuneInString(text[i:])
			switch c {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			default:
				out.WriteString(text[i : i+size])
			}
			i += size
			continue
		}
		out.WriteString(text[i : i+size])
		i += size
	}
	return out.String()
}

// Grammar is the syntax tree of any of the four notations: the statements before the rules,
// and the rules.
type Grammar struct {
	title, accTitle, accDescr string
	rules                     []*astRule
}

func (g *Grammar) meta() parsed { return parsed{g.title, g.accTitle, g.accDescr} }

type astRule struct {
	name       string
	definition *astNode
}

// astNode is a node of any of the four syntax trees. typ is its $type; the other fields are the
// union of what the types hold.
type astNode struct {
	typ          string
	value, name  string
	text, dot    string
	operator     string
	hasOperator  bool
	repeat       string
	hasRepeat    bool
	alternatives []*astNode
	elements     []*astNode
	element      *astNode
	primary      *astNode
	suffix       *astNode
	base         *astNode
	postfixes    []*astNode
	except       *astNode
}

// parseGrammar parses the keyword, then any titles and accessibility statements, then rules to the end of the text.
// Every table starts with its keyword and has TITLE, ACC_TITLE, and ACC_DESCR in a row from `meta`.
func parseGrammar(src string, tokenTypes []*common.TokenType, meta int, unquote func(string) string, rule func(r *common.Reader) *astRule) *Grammar {
	r := common.NewReader(common.Tokenize(src, tokenTypes, "railroad"), tokenTypes, "railroad")
	ast := &Grammar{}
	r.Expect(0)
	for {
		kind := r.Kind()
		if kind == meta {
			title := common.TitleValue(r.Take())
			if title != "" && (title[0] == '"' || title[0] == '\'') && strings.HasSuffix(title, title[:1]) {
				ast.title = unquote(title)
			} else {
				ast.title = title
			}
		} else if kind == meta+1 {
			ast.accTitle = common.AccTitleValue(r.Take())
		} else if kind == meta+2 {
			ast.accDescr = common.AccDescrValue(r.Take())
		} else {
			break
		}
	}
	for r.Kind() != -1 {
		ast.rules = append(ast.rules, rule(r))
	}
	return ast
}

// parts are the fields that hold a node's parts, in any of the four syntax trees.
func parts(p *astNode) []*astNode {
	if p.base != nil {
		list := []*astNode{p.base}
		for _, postfix := range p.postfixes {
			if postfix.except != nil {
				list = append(list, postfix.except)
			}
		}
		return list
	}
	if p.alternatives != nil {
		return p.alternatives
	}
	if p.elements != nil {
		return p.elements
	}
	one := p.element
	if one == nil {
		one = p.primary
	}
	if one == nil {
		one = p.suffix
	}
	if one != nil {
		return []*astNode{one}
	}
	return nil
}

// modelParts are the parts of a node of the rule model.
func modelParts(n *RailroadNode) []*RailroadNode {
	switch n.typ {
	case "choice":
		return n.alternatives
	case "sequence":
		return n.elements
	case "optional", "repetition":
		return []*RailroadNode{n.element}
	}
	return nil
}

func terminal(value string) *RailroadNode   { return &RailroadNode{typ: "terminal", value: value} }
func nonterminal(name string) *RailroadNode { return &RailroadNode{typ: "nonterminal", name: name} }
func special(text string) *RailroadNode     { return &RailroadNode{typ: "special", text: text} }
func optional(element *RailroadNode) *RailroadNode {
	return &RailroadNode{typ: "optional", element: element}
}
func repetition(element *RailroadNode, min, max float64) *RailroadNode {
	return &RailroadNode{typ: "repetition", element: element, min: min, max: max}
}

// sequence: a sequence or a choice of one is that one, in every notation.
func sequence(elements []*RailroadNode) *RailroadNode {
	if len(elements) == 1 {
		return elements[0]
	}
	return &RailroadNode{typ: "sequence", elements: elements}
}

func choice(alternatives []*RailroadNode) *RailroadNode {
	if len(alternatives) == 1 {
		return alternatives[0]
	}
	return &RailroadNode{typ: "choice", alternatives: alternatives}
}

// toRules turns each rule's syntax tree into the common model, given how one node is built from its parts.
func toRules(ast *Grammar, build func(part *astNode, kids []*RailroadNode) *RailroadNode) []RailroadRule {
	out := make([]RailroadRule, len(ast.rules))
	for i, rule := range ast.rules {
		out[i] = RailroadRule{name: rule.name, definition: fold(rule.definition, parts, build)}
	}
	return out
}

// fold rebuilds a tree bottom-up without recursion, so that nesting depth is bounded by memory, not by the stack.
func fold[T, R any](root T, children func(T) []T, build func(T, []R) R) R {
	nodes := []T{root}
	lists := [][]T{children(root)}
	done := [][]R{nil}
	for {
		top := len(nodes) - 1
		results := done[top]
		if len(results) < len(lists[top]) {
			child := lists[top][len(results)]
			nodes = append(nodes, child)
			lists = append(lists, children(child))
			done = append(done, nil)
			continue
		}
		value := build(nodes[top], results)
		if top == 0 {
			return value
		}
		nodes = nodes[:top]
		lists = lists[:top]
		done = done[:top]
		done[top-1] = append(done[top-1], value)
	}
}
