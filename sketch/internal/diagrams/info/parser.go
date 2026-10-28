// Ported from pele src/diagrams/info/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package info

import "tuios.dev/sketch/internal/diagrams/common"

const (
	T_showInfo = iota
	T_info
	T_accDescr
	T_accTitle
	T_title
	T_newline
)

var INFO_TOKENS = []*common.TokenType{
	common.Keyword("showInfo"),
	common.Keyword("info"),
	common.ACC_DESCR,
	common.ACC_TITLE,
	common.TITLE,
	common.NEWLINE,
	common.WHITESPACE,
	common.YAML,
	common.DIRECTIVE,
	common.SINGLE_LINE_COMMENT,
}

type InfoAst struct {
	title    string
	accTitle string
	accDescr string
}

func parseInfo(src string) *InfoAst {
	r := common.NewReader(common.Tokenize(src, INFO_TOKENS, "info"), INFO_TOKENS, "info")
	ast := &InfoAst{}

	for r.Kind() == T_newline {
		r.I++
	}
	r.Expect(T_info)
	for r.Kind() == T_newline {
		r.I++
	}
	if r.Accept(T_showInfo) {
		for r.Kind() == T_newline {
			r.I++
		}
	}
	for r.Kind() != -1 {
		switch r.Kind() {
		case T_accDescr:
			ast.accDescr = common.AccDescrValue(r.Take())
		case T_accTitle:
			ast.accTitle = common.AccTitleValue(r.Take())
		case T_title:
			ast.title = common.TitleValue(r.Take())
		default:
			r.Fail("a title or the end of input")
		}
		if r.Kind() == -1 {
			break
		}
		if r.Kind() != T_newline {
			r.Fail("a line break or the end of input")
		}
		for r.Kind() == T_newline {
			r.I++
		}
	}
	return ast
}
