// Ported from pele src/diagrams/architecture/parser.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The sticky regular expressions are hand matchers.

package architecture

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

const (
	T_header = iota
	T_junction
	T_service
	T_column
	T_group
	T_align
	T_row
	T_dashes
	T_in
	T_colon
	T_dash
	T_direction
	T_arrowGroup
	T_into
	T_accDescr
	T_accTitle
	T_title
	T_string
	T_id
	T_newline
	T_whitespace
	T_yaml
	T_directive
	T_comment
	T_icon
	T_label
)

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// matchID is /[\w]([-\w]*\w)?/y: a word character, then the longest run of [-\w] that ends in \w.
func matchID(src string, p int) int {
	if p >= len(src) || !isWordByte(src[p]) {
		return -1
	}
	end := p + 1
	for q := p + 1; q < len(src); q++ {
		c := src[q]
		if isWordByte(c) {
			end = q + 1
		} else if c != '-' {
			break
		}
	}
	return end
}

// matchDirection is /L|R|T|B/y.
func matchDirection(src string, p int) int {
	if p < len(src) && strings.IndexByte("LRTB", src[p]) >= 0 {
		return p + 1
	}
	return -1
}

// matchInto is /<|>/y.
func matchInto(src string, p int) int {
	if p < len(src) && (src[p] == '<' || src[p] == '>') {
		return p + 1
	}
	return -1
}

// matchIcon is /\([\w-:]+\)/y. Without the u flag, `\w-:` in a class is \w, a hyphen and a colon.
func matchIcon(src string, p int) int {
	if p >= len(src) || src[p] != '(' {
		return -1
	}
	q := p + 1
	for q < len(src) && (isWordByte(src[q]) || src[q] == '-' || src[q] == ':') {
		q++
	}
	if q > p+1 && q < len(src) && src[q] == ')' {
		return q + 1
	}
	return -1
}

// quotedEnd is the end of `"([^"\\]|\\.)*"` (or the same with single quotes) at q, or -1.
// The closing quote is the first one not escaped, and `.` takes no line terminator.
func quotedEnd(src string, q int) int {
	quote := src[q]
	for i := q + 1; i < len(src); {
		c := src[i]
		if c == quote {
			return i + 1
		}
		if c == '\\' {
			if i+1 >= len(src) {
				return -1
			}
			r, size := rune(src[i+1]), 1
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(src[i+1:])
			}
			if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 {
				return -1
			}
			i += 1 + size
			continue
		}
		i++
	}
	return -1
}

// matchTitle is /\[(?:"([^"\\]|\\.)*"|'([^'\\]|\\.)*'|[^\[\]\r\n]+)\]/y.
func matchTitle(src string, p int) int {
	if p >= len(src) || src[p] != '[' {
		return -1
	}
	q := p + 1
	if q < len(src) && (src[q] == '"' || src[q] == '\'') {
		if e := quotedEnd(src, q); e >= 0 && e < len(src) && src[e] == ']' {
			return e + 1
		}
	}
	e := q
	for e < len(src) && src[e] != '[' && src[e] != ']' && src[e] != '\r' && src[e] != '\n' {
		e++
	}
	if e > q && e < len(src) && src[e] == ']' {
		return e + 1
	}
	return -1
}

var ID = &common.TokenType{Name: "ID", Match: matchID, First: "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"}
var NAME = []*common.TokenType{ID}
var LONGER_YAML = []*common.TokenType{common.YAML}

func word(text string) *common.TokenType {
	return &common.TokenType{Name: text, Pattern: text, Longer: NAME}
}

// ARCHITECTURE_TOKENS: the direction letters come before identifiers and have no longer alternative, so Mermaid
// reads an id that starts with L, R, T or B as a direction followed by the rest.
var ARCHITECTURE_TOKENS = []*common.TokenType{
	word("architecture-beta"),
	word("junction"),
	word("service"),
	word("column"),
	word("group"),
	word("align"),
	word("row"),
	{Name: "--", Pattern: "--", Longer: LONGER_YAML},
	word("in"),
	{Name: ":", Pattern: ":"},
	{Name: "-", Pattern: "-", Longer: LONGER_YAML},
	{Name: "ARROW_DIRECTION", Match: matchDirection, First: "LRTB"},
	{Name: "ARROW_GROUP", Pattern: "{group}"},
	{Name: "ARROW_INTO", Match: matchInto, First: "<>"},
	common.ACC_DESCR,
	common.ACC_TITLE,
	common.TitleBefore(common.RunEndsIn("-", "")),
	common.STRING,
	ID,
	common.NEWLINE,
	common.WHITESPACE,
	common.YAML,
	common.DIRECTIVE,
	common.SINGLE_LINE_COMMENT,
	{Name: "ARCH_ICON", Match: matchIcon, First: "("},
	{Name: "ARCH_TITLE", Match: matchTitle, First: "["},
}

type GroupAst struct {
	id    string
	icon  string
	title string
	in    string
}

type ServiceAst struct {
	id       string
	icon     string
	iconText string
	title    string
	in       string
}

type JunctionAst struct {
	id string
	in string
}

type EdgeAst struct {
	lhsId    string
	lhsGroup bool
	lhsDir   string
	lhsInto  bool
	title    string
	rhsInto  bool
	rhsDir   string
	rhsId    string
	rhsGroup bool
}

type AlignmentAst struct {
	direction string
	members   []string
}

type ArchitectureAst struct {
	title      string
	accTitle   string
	accDescr   string
	groups     []GroupAst
	services   []ServiceAst
	junctions  []JunctionAst
	edges      []EdgeAst
	alignments []AlignmentAst
}

// labelValue is Mermaid's ArchitectureValueConverter for ARCH_TITLE.
func labelValue(text string) string {
	result := js.Trim(text[1 : len(text)-1])
	if (strings.HasPrefix(result, `"`) && strings.HasSuffix(result, `"`)) || (strings.HasPrefix(result, "'") && strings.HasSuffix(result, "'")) {
		if len(result) < 2 {
			result = ""
		} else {
			result = result[1 : len(result)-1]
		}
		result = strings.ReplaceAll(strings.ReplaceAll(result, `\"`, `"`), `\'`, "'")
	}
	return js.Trim(result)
}

func parseArchitecture(src string) *ArchitectureAst {
	r := common.NewReader(common.Tokenize(src, ARCHITECTURE_TOKENS, "architecture"), ARCHITECTURE_TOKENS, "architecture")
	ast := &ArchitectureAst{}

	is := func(kind int) bool { return r.Kind() == kind }
	endOfLine := func() {
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

	for r.Kind() == T_newline {
		r.I++
	}
	r.Expect(T_header)
	for r.Kind() != -1 {
		switch r.Kind() {
		case T_newline:
			r.I++
			continue
		case T_accDescr:
			ast.accDescr = common.AccDescrValue(r.Take())
		case T_accTitle:
			ast.accTitle = common.AccTitleValue(r.Take())
		case T_title:
			ast.title = common.TitleValue(r.Take())
		case T_group:
			r.I++
			group := GroupAst{id: r.Expect(T_id)}
			if is(T_icon) {
				t := r.Take()
				group.icon = t[1 : len(t)-1]
			}
			if is(T_label) {
				group.title = labelValue(r.Take())
			}
			if r.Accept(T_in) {
				group.in = r.Expect(T_id)
			}
			ast.groups = append(ast.groups, group)
		case T_service:
			r.I++
			service := ServiceAst{id: r.Expect(T_id)}
			if is(T_string) {
				service.iconText = common.StringValue(r.Take())
			} else if is(T_icon) {
				t := r.Take()
				service.icon = t[1 : len(t)-1]
			}
			if is(T_label) {
				service.title = labelValue(r.Take())
			}
			if r.Accept(T_in) {
				service.in = r.Expect(T_id)
			}
			ast.services = append(ast.services, service)
		case T_junction:
			r.I++
			junction := JunctionAst{id: r.Expect(T_id)}
			if r.Accept(T_in) {
				junction.in = r.Expect(T_id)
			}
			ast.junctions = append(ast.junctions, junction)
		case T_id:
			lhsId := r.Take()
			lhsGroup := r.Accept(T_arrowGroup)
			r.Expect(T_colon)
			lhsDir := r.Expect(T_direction)
			lhsInto := r.Accept(T_into)
			title := ""
			if !r.Accept(T_dashes) {
				if !is(T_dash) {
					r.Fail("'--' or '-'")
				}
				r.I++
				title = labelValue(r.Expect(T_label))
				r.Expect(T_dash)
			}
			rhsInto := r.Accept(T_into)
			rhsDir := r.Expect(T_direction)
			r.Expect(T_colon)
			rhsId := r.Expect(T_id)
			rhsGroup := r.Accept(T_arrowGroup)
			ast.edges = append(ast.edges, EdgeAst{lhsId: lhsId, lhsGroup: lhsGroup, lhsDir: lhsDir, lhsInto: lhsInto, title: title,
				rhsInto: rhsInto, rhsDir: rhsDir, rhsId: rhsId, rhsGroup: rhsGroup})
		case T_align:
			r.I++
			if !is(T_row) && !is(T_column) {
				r.Fail("'row' or 'column'")
			}
			direction := r.Take()
			members := []string{r.Expect(T_id), r.Expect(T_id)}
			for is(T_id) {
				members = append(members, r.Take())
			}
			ast.alignments = append(ast.alignments, AlignmentAst{direction: direction, members: members})
		default:
			r.Fail("a group, a service, a junction, an edge, an alignment, a title, or a line break")
		}
		endOfLine()
	}
	return ast
}
