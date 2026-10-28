// Ported from pele src/diagrams/cynefin/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package cynefin

import (
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
)

const (
	T_keywordColon = iota
	T_keyword
	T_arrow
	T_colon
	T_domain
	T_accDescr
	T_accTitle
	T_title
	T_string
	T_newline
)

// matchDomain answers as /complex|complicated|clear|chaotic|confusion/y.
func matchDomain(src string, p int) int {
	for _, word := range [...]string{"complex", "complicated", "clear", "chaotic", "confusion"} {
		if strings.HasPrefix(src[p:], word) {
			return p + len(word)
		}
	}
	return -1
}

var CYNEFIN_TOKENS = []*common.TokenType{
	{Name: "cynefin-beta:", Pattern: "cynefin-beta:"},
	common.Keyword("cynefin-beta"),
	{Name: "-->", Pattern: "-->"},
	{Name: ":", Pattern: ":"},
	{Name: "DOMAIN_NAME", Match: matchDomain},
	common.ACC_DESCR,
	common.ACC_TITLE,
	common.TITLE,
	common.STRING,
	common.NEWLINE,
	common.WHITESPACE,
	common.YAML,
	common.DIRECTIVE,
	common.SINGLE_LINE_COMMENT,
}

type DomainName = string

type DomainBlockAst struct {
	domain DomainName
	items  []string
}

type TransitionAst struct {
	from  DomainName
	to    DomainName
	label string
}

type CynefinAst struct {
	title       string
	accTitle    string
	accDescr    string
	domains     []DomainBlockAst
	transitions []TransitionAst
}

func parseCynefin(src string) *CynefinAst {
	r := common.NewReader(common.Tokenize(src, CYNEFIN_TOKENS, "cynefin"), CYNEFIN_TOKENS, "cynefin")
	ast := &CynefinAst{}

	skipLines := func() {
		for r.Kind() == T_newline {
			r.I++
		}
	}
	endOfLine := func() {
		if r.Kind() == -1 {
			return
		}
		if r.Kind() != T_newline {
			r.Fail("a line break or the end of input")
		}
		skipLines()
	}

	skipLines()
	if r.Kind() != T_keyword && r.Kind() != T_keywordColon {
		r.Fail("'cynefin-beta'")
	}
	r.I++
	for r.Kind() != -1 {
		switch r.Kind() {
		case T_newline:
			r.I++
		case T_accDescr:
			ast.accDescr = common.AccDescrValue(r.Take())
			endOfLine()
		case T_accTitle:
			ast.accTitle = common.AccTitleValue(r.Take())
			endOfLine()
		case T_title:
			ast.title = common.TitleValue(r.Take())
			endOfLine()
		case T_domain:
			name := r.Take()
			if r.Accept(T_arrow) {
				transition := TransitionAst{from: name, to: r.Expect(T_domain)}
				if r.Accept(T_colon) {
					transition.label = common.StringValue(r.Expect(T_string))
				}
				endOfLine()
				ast.transitions = append(ast.transitions, transition)
				break
			}
			block := DomainBlockAst{domain: name}
			skipLines()
			for r.Kind() == T_string {
				block.items = append(block.items, common.StringValue(r.Take()))
				skipLines()
			}
			ast.domains = append(ast.domains, block)
		default:
			r.Fail("a domain, a transition, a title, or a line break")
		}
	}
	return ast
}
