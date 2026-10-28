// Ported from pele src/diagrams/pie/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package pie

import (
	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

const (
	T_showData = iota
	T_pie
	T_colon
	T_number
	T_accDescr
	T_accTitle
	T_title
	T_string
	T_newline
)

func charCode(src string, i int) int {
	if i < 0 || i >= len(src) {
		return -1
	}
	return int(src[i])
}

func digits(src string, from int) int {
	i := from
	for c := charCode(src, i); c >= 48 && c <= 57; c = charCode(src, i) {
		i++
	}
	return i
}

type numberScanner struct {
	from     int
	end      int
	fraction int
}

// Answers as NUMBER_PIE's pattern does. The pattern reads a whole run of digits before it finds
// there is no fraction, and would do so again from every digit of a long run of zeros.
func newNumberScanner() common.Scanner {
	s := &numberScanner{fraction: -1}
	return s.scan
}

func (s *numberScanner) scan(src string, p int) int {
	at := p
	if charCode(src, p) == 45 {
		at = p + 1
	}
	if at < s.from || at >= s.end {
		s.from = at
		s.end = digits(src, at)
		s.fraction = -1
		if s.end > at && charCode(src, s.end) == 46 {
			stop := digits(src, s.end+1)
			length := stop - s.end - 1
			if length > 0 && charCode(src, stop) != 46 {
				s.fraction = stop
			} else if length > 1 {
				s.fraction = stop - 1
			}
		}
	}
	if s.end == at {
		return -1
	}
	if s.fraction >= 0 {
		return s.fraction
	}
	if charCode(src, at) == 48 {
		if charCode(src, at+1) == 46 {
			return -1
		}
		return at + 1
	}
	if charCode(src, s.end) != 46 {
		return s.end
	}
	if s.end-1 > at {
		return s.end - 1
	}
	return -1
}

var PIE_TOKENS = []*common.TokenType{
	common.Keyword("showData"),
	common.Keyword("pie"),
	{Name: ":", Pattern: ":"},
	{Name: "NUMBER_PIE", Scanner: newNumberScanner, First: "-0123456789"},
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

type PieSection struct {
	label string
	value float64
}

type PieAst struct {
	showData bool
	title    string
	accTitle string
	accDescr string
	sections []PieSection
}

type pieParser struct {
	r *common.Reader
}

func (p *pieParser) endOfLine() {
	r := p.r
	if r.Kind() == -1 {
		return
	}
	if r.Kind() != T_newline {
		r.Fail("a line break or the end of input")
	}
	for r.Kind() == T_newline {
		r.I++
	}
}

func parsePie(src string) *PieAst {
	r := common.NewReader(common.Tokenize(src, PIE_TOKENS, "pie"), PIE_TOKENS, "pie")
	p := &pieParser{r}
	ast := &PieAst{}

	for r.Kind() == T_newline {
		r.I++
	}
	r.Expect(T_pie)
	ast.showData = r.Accept(T_showData)
	for r.Kind() != -1 {
		switch r.Kind() {
		case T_newline:
			r.I++
		case T_accDescr:
			ast.accDescr = common.AccDescrValue(r.Take())
			p.endOfLine()
		case T_accTitle:
			ast.accTitle = common.AccTitleValue(r.Take())
			p.endOfLine()
		case T_title:
			ast.title = common.TitleValue(r.Take())
			p.endOfLine()
		case T_string:
			label := common.StringValue(r.Take())
			r.Expect(T_colon)
			value := js.Number(r.Expect(T_number))
			ast.sections = append(ast.sections, PieSection{label, value})
			p.endOfLine()
		default:
			r.Fail("a section, a title, or a line break")
		}
	}
	return ast
}
