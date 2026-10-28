// Ported from pele src/diagrams/treemap/parser.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The token patterns are hand matchers. CLASS_DEF and RE_CLASS_DEF match the same text: the
// name takes two or more word characters, and `\s+` after it may cross a line break.

package treemap

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

const (
	T_accDescr = iota
	T_accTitle
	T_title
	T_keyword
	T_classDef
	T_styleSeparator
	T_separator
	T_comma
	T_indentation
	T_whitespace
	T_comment
	T_newline
	T_id
	T_number
	T_string
)

func spaceRun(src string, q int) int {
	for q < len(src) {
		r, size := rune(src[q]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(src[q:])
		}
		if !js.IsSpace(r) {
			break
		}
		q += size
	}
	return q
}

func isIDStart(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }
func isIDPart(c byte) bool  { return isIDStart(c) || c >= '0' && c <= '9' }

// classDef matches /classDef\s+([a-zA-Z_][a-zA-Z0-9_]+)(?:\s+([^;\r\n]*))?(?:;)?/y at p: the end,
// the class name, and the style text with whether its group matched. end is -1 for no match.
func classDef(src string, p int) (end int, name, style string, hasStyle bool) {
	if !strings.HasPrefix(src[p:], "classDef") {
		return -1, "", "", false
	}
	q := p + len("classDef")
	s := spaceRun(src, q)
	if s == q || s >= len(src) || !isIDStart(src[s]) {
		return -1, "", "", false
	}
	e := s + 1
	for e < len(src) && isIDPart(src[e]) {
		e++
	}
	if e-s < 2 {
		return -1, "", "", false
	}
	name = src[s:e]
	q = e
	if w := spaceRun(src, q); w > q {
		t := w
		for t < len(src) && src[t] != ';' && src[t] != '\r' && src[t] != '\n' {
			t++
		}
		style, hasStyle = src[w:t], true
		q = t
	}
	if q < len(src) && src[q] == ';' {
		q++
	}
	return q, name, style, hasStyle
}

func matchClassDef(src string, p int) int {
	end, _, _, _ := classDef(src, p)
	return end
}

func matchKeyword(src string, p int) int {
	switch {
	case strings.HasPrefix(src[p:], "treemap-beta"):
		return p + len("treemap-beta")
	case strings.HasPrefix(src[p:], "treemap"):
		return p + len("treemap")
	}
	return -1
}

func matchBlanks(src string, p int) int {
	q := p
	for q < len(src) && (src[q] == ' ' || src[q] == '\t') {
		q++
	}
	if q == p {
		return -1
	}
	return q
}

func matchComment(src string, p int) int {
	if !strings.HasPrefix(src[p:], "%%") {
		return -1
	}
	if e := strings.IndexByte(src[p:], '\n'); e >= 0 {
		return p + e
	}
	return len(src)
}

func matchNewline(src string, p int) int {
	if strings.HasPrefix(src[p:], "\r\n") {
		return p + 2
	}
	if p < len(src) && src[p] == '\n' {
		return p + 1
	}
	return -1
}

func matchID(src string, p int) int {
	if p >= len(src) || !isIDStart(src[p]) {
		return -1
	}
	q := p + 1
	for q < len(src) && isIDPart(src[q]) {
		q++
	}
	return q
}

func matchNumber(src string, p int) int {
	q := p
	for q < len(src) && (src[q] >= '0' && src[q] <= '9' || src[q] == '_' || src[q] == '.' || src[q] == ',') {
		q++
	}
	if q == p {
		return -1
	}
	return q
}

func matchString(src string, p int) int {
	if p >= len(src) || (src[p] != '"' && src[p] != '\'') {
		return -1
	}
	if e := strings.IndexByte(src[p+1:], src[p]); e >= 0 {
		return p + 1 + e + 1
	}
	return -1
}

const idChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"

// TREEMAP_TOKENS: Mermaid's treemap grammar has its own terminals. Every run of spaces or tabs is an INDENTATION
// token, since that comes before WS, and line breaks are hidden, so rows are told apart by their
// tokens alone and the indentation of a row is whatever whitespace precedes it.
var TREEMAP_TOKENS = []*common.TokenType{
	common.ACC_DESCR,
	common.ACC_TITLE,
	common.TitleBefore(common.WordNext),
	{Name: "TREEMAP_KEYWORD", Match: matchKeyword, First: "t"},
	{Name: "CLASS_DEF", Match: matchClassDef, First: "c"},
	{Name: "STYLE_SEPARATOR", Pattern: ":::"},
	{Name: "SEPARATOR", Pattern: ":"},
	{Name: "COMMA", Pattern: ","},
	{Name: "INDENTATION", Match: matchBlanks, First: " \t"},
	{Name: "WS", Match: matchBlanks, Hidden: true, First: " \t"},
	{Name: "ML_COMMENT", Match: matchComment, Hidden: true, First: "%"},
	{Name: "NL", Match: matchNewline, Hidden: true, First: "\r\n"},
	{Name: "ID2", Match: matchID, First: idChars},
	{Name: "NUMBER2", Match: matchNumber, First: "0123456789_.,"},
	{Name: "STRING2", Match: matchString, First: `"'`},
}

type TreemapItemAst struct {
	leaf          bool
	name          string
	value         float64
	classSelector string
}

// TreemapRowAst is a TreemapRow, or a ClassDefStatement when classDef is set.
type TreemapRowAst struct {
	classDef  bool
	indent    int
	hasIndent bool
	item      TreemapItemAst
	className string
	styleText string
}

type TreemapAst struct {
	title       string
	accTitle    string
	accDescr    string
	TreemapRows []TreemapRowAst
}

func parseTreemap(src string) *TreemapAst {
	r := common.NewReader(common.Tokenize(src, TREEMAP_TOKENS, "treemap"), TREEMAP_TOKENS, "treemap")
	kinds := r.Kinds
	ast := &TreemapAst{}

	classSelector := func() string {
		if r.Accept(T_styleSeparator) {
			return r.Expect(T_id)
		}
		return ""
	}

	r.Expect(T_keyword)
	for r.Kind() != -1 {
		kind := r.Kind()
		if kind == T_accDescr {
			ast.accDescr = common.AccDescrValue(r.Take())
			continue
		}
		if kind == T_accTitle {
			ast.accTitle = common.AccTitleValue(r.Take())
			continue
		}
		if kind == T_title {
			ast.title = common.TitleValue(r.Take())
			continue
		}
		if kind != T_indentation && kind != T_string && kind != T_classDef {
			r.Fail("a node, a class definition, or a title")
		}

		var row TreemapRowAst
		if kind == T_indentation {
			row.indent, row.hasIndent = len(r.Take()), true
		}
		if r.Kind() == T_classDef {
			_, name, style, _ := classDef(r.Take(), 0)
			row.classDef, row.className, row.styleText = true, name, style
			ast.TreemapRows = append(ast.TreemapRows, row)
			continue
		}

		name := r.Expect(T_string)
		name = name[1 : len(name)-1]
		// A leaf is a name followed by a colon or comma, with or without whitespace before it.
		after := r.I
		if kinds[r.I] == T_indentation {
			after = r.I + 1
		}
		item := TreemapItemAst{name: name}
		if after < len(kinds) && (kinds[after] == T_separator || kinds[after] == T_comma) {
			r.I = after + 1
			r.Accept(T_indentation)
			item.leaf = true
			item.value = js.ParseFloat(strings.ReplaceAll(r.Expect(T_number), ",", ""))
		}
		item.classSelector = classSelector()
		row.item = item
		ast.TreemapRows = append(ast.TreemapRows, row)
	}
	return ast
}
