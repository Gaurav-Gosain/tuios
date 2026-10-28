// Ported from pele src/diagrams/treeview/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package treeview

import (
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/util"
)

const (
	T_keyword = iota
	T_accDescr
	T_accTitle
	T_title
	T_classAnnotation
	T_iconAnnotation
	T_descAnnotation
	T_indentation
	T_quotedName
	T_ws
	T_comment
	T_newline
	T_bareName
)

// charAt is src.charCodeAt(i) for the ASCII tests here: -1 past the end. A byte of a character
// outside ASCII equals no ASCII character, so bytes stand for code units.
func charAt(src string, i int) int {
	if i < 0 || i >= len(src) {
		return -1
	}
	return int(src[i])
}

func isBlank(c int) bool { return c == 32 || c == 9 }

func annotationAt(src string, at int) bool {
	if strings.HasPrefix(src[at:], "##") || strings.HasPrefix(src[at:], "icon(") {
		return true
	}
	if !strings.HasPrefix(src[at:], ":::") {
		return false
	}
	i := at + 3
	for isBlank(charAt(src, i)) {
		i++
	}
	c := charAt(src, i)
	return (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95
}

// The pattern looks ahead from every blank for an annotation, which rescans long runs of blanks.
// This looks once per run.
func matchBareName(src string, at int) int {
	first := charAt(src, at)
	if isBlank(first) || first == 10 || first == 13 || first == 34 || first == 39 {
		return -1
	}
	if strings.HasPrefix(src[at:], ":::") || strings.HasPrefix(src[at:], "icon(") || strings.HasPrefix(src[at:], "##") {
		return -1
	}
	n := len(src)
	i := at + 1
	for i < n {
		c := int(src[i])
		if c == 10 || c == 13 {
			break
		}
		if isBlank(c) {
			j := i + 1
			for isBlank(charAt(src, j)) {
				j++
			}
			if annotationAt(src, j) {
				break
			}
			i = j
		} else {
			i++
		}
	}
	return i
}

// blanksFrom is the end of /[ \t]+/ at p, or -1.
func blanksFrom(src string, p int) int {
	q := p
	for isBlank(charAt(src, q)) {
		q++
	}
	if q == p {
		return -1
	}
	return q
}

// isWordOrDash is [\w-].
func isWordOrDash(c int) bool { return c == '-' || (c >= 0 && util.IsWord(rune(c))) }

// /[ \t]+:::[ \t]*[A-Za-z_][\w-]*/y
func matchClassAnnotation(src string, p int) int {
	q := blanksFrom(src, p)
	if q < 0 || !strings.HasPrefix(src[q:], ":::") {
		return -1
	}
	q += 3
	for isBlank(charAt(src, q)) {
		q++
	}
	if c := charAt(src, q); !((c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95) {
		return -1
	}
	q++
	for isWordOrDash(charAt(src, q)) {
		q++
	}
	return q
}

// /[ \t]+icon\([\w-]*(?::[\w-]+)?\)/y
func matchIconAnnotation(src string, p int) int {
	q := blanksFrom(src, p)
	if q < 0 || !strings.HasPrefix(src[q:], "icon(") {
		return -1
	}
	q += 5
	for isWordOrDash(charAt(src, q)) {
		q++
	}
	if charAt(src, q) == ':' && isWordOrDash(charAt(src, q+1)) {
		q++
		for isWordOrDash(charAt(src, q)) {
			q++
		}
	}
	if charAt(src, q) != ')' {
		return -1
	}
	return q + 1
}

// /[ \t]+##[^\n\r]*/y
func matchDescAnnotation(src string, p int) int {
	q := blanksFrom(src, p)
	if q < 0 || !strings.HasPrefix(src[q:], "##") {
		return -1
	}
	q += 2
	for q < len(src) && src[q] != '\n' && src[q] != '\r' {
		q++
	}
	return q
}

// /"[^"]*"|'[^']*'/y
func matchQuotedName(src string, p int) int {
	quote := src[p]
	if quote != '"' && quote != '\'' {
		return -1
	}
	if end := strings.IndexByte(src[p+1:], quote); end >= 0 {
		return p + 1 + end + 1
	}
	return -1
}

// /\%\%[^\n]*/y
func matchComment(src string, p int) int {
	if !strings.HasPrefix(src[p:], "%%") {
		return -1
	}
	if end := strings.IndexByte(src[p:], '\n'); end >= 0 {
		return p + end
	}
	return len(src)
}

// /\r?\n/y
func matchNewline(src string, p int) int {
	if src[p] == '\n' {
		return p + 1
	}
	if strings.HasPrefix(src[p:], "\r\n") {
		return p + 2
	}
	return -1
}

var BARE_NAME = &common.TokenType{Name: "BARE_NAME", Match: matchBareName}

var TREEVIEW_TOKENS = []*common.TokenType{
	func() *common.TokenType {
		kw := *common.Keyword("treeView-beta")
		kw.Longer = []*common.TokenType{BARE_NAME}
		return &kw
	}(),
	common.AccDescr(""),
	common.ACC_TITLE,
	common.TitleBefore(titleRest),
	{Name: "CLASS_ANNOTATION", Match: matchClassAnnotation},
	{Name: "ICON_ANNOTATION", Match: matchIconAnnotation},
	{Name: "DESC_ANNOTATION", Match: matchDescAnnotation},
	{Name: "INDENTATION", Match: func(src string, p int) int { return blanksFrom(src, p) }},
	{Name: "QUOTED_NAME", Match: matchQuotedName},
	{Name: "WS", Match: func(src string, p int) int { return blanksFrom(src, p) }, Hidden: true},
	{Name: "ML_COMMENT", Match: matchComment, Hidden: true},
	{Name: "NL", Match: matchNewline, Hidden: true},
	BARE_NAME,
}

type TreeNodeAst struct {
	indent          int
	hasIndent       bool
	name            string
	classAnnotation string
	hasClass        bool
	iconAnnotation  string
	hasIcon         bool
	descAnnotation  string
}

type TreeViewAst struct {
	title    string
	accTitle string
	accDescr string
	nodes    []TreeNodeAst
}

func trimBlanksEnd(text string) string {
	end := len(text)
	for end > 0 && isBlank(int(text[end-1])) {
		end--
	}
	return text[:end]
}

// Mermaid's TreeViewValueConverter, by terminal name, for the annotations.
func convertValue(rule string, input string) string {
	switch rule {
	case "CLASS_ANNOTATION":
		return js.Trim(js.Trim(input)[3:])
	case "ICON_ANNOTATION":
		trimmed := js.Trim(input)
		return trimmed[5 : len(trimmed)-1]
	case "DESC_ANNOTATION":
		return js.Trim(js.Trim(input)[2:])
	}
	return ""
}

func parseTreeView(src string) *TreeViewAst {
	r := common.NewReader(common.Tokenize(src, TREEVIEW_TOKENS, "treeView"), TREEVIEW_TOKENS, "treeView")
	ast := &TreeViewAst{}
	r.Expect(T_keyword)
	for {
		if r.Kind() == T_accDescr {
			ast.accDescr = common.AccDescrValue(r.Take())
		} else if r.Kind() == T_accTitle {
			ast.accTitle = common.AccTitleValue(r.Take())
		} else if r.Kind() == T_title {
			ast.title = common.TitleValue(r.Take())
		} else {
			break
		}
	}
	for r.Kind() != -1 {
		var node TreeNodeAst
		if r.Kind() == T_indentation {
			node.indent, node.hasIndent = len(r.Take()), true
		}
		if r.Kind() == T_quotedName {
			text := r.Take()
			node.name = text[1 : len(text)-1]
		} else if r.Kind() == T_bareName {
			node.name = trimBlanksEnd(r.Take())
		} else {
			r.Fail("a name")
		}
		for {
			if r.Kind() == T_classAnnotation {
				node.classAnnotation, node.hasClass = convertValue("CLASS_ANNOTATION", r.Take()), true
			} else if r.Kind() == T_iconAnnotation {
				node.iconAnnotation, node.hasIcon = convertValue("ICON_ANNOTATION", r.Take()), true
			} else if r.Kind() == T_descAnnotation {
				node.descAnnotation = convertValue("DESC_ANNOTATION", r.Take())
			} else {
				break
			}
		}
		ast.nodes = append(ast.nodes, node)
	}
	return ast
}

// titleRest is the lookahead (?!%%)[^\n\r]: more of the line, not a comment.
func titleRest(src string, q int) bool {
	return q < len(src) && src[q] != '\n' && src[q] != '\r' && !strings.HasPrefix(src[q:], "%%")
}
