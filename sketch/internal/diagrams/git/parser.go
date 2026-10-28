// Ported from pele src/diagrams/git/parser.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The two sticky regular expressions are hand matchers: INT has a lookahead.

package git

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/util"
)

const (
	T_cherryPick = iota
	T_gitGraphColon
	T_highlight
	T_gitGraph
	T_checkout
	T_reverse
	T_parent
	T_commit
	T_normal
	T_branch
	T_order
	T_switch
	T_type
	T_merge
	T_msg
	T_tag
	T_id
	T_lr
	T_tb
	T_bt
	T_colon
	T_accDescr
	T_accTitle
	T_title
	T_int
	T_string
	T_newline
	T_reference = 31
)

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// matchReference is /\w([-\./\w]*[-\w])?/y: a word character, then the longest run of
// [-./\w] that ends in [-\w].
func matchReference(src string, p int) int {
	if p >= len(src) || !isWordByte(src[p]) {
		return -1
	}
	end := p + 1
	for q := p + 1; q < len(src); q++ {
		c := src[q]
		if isWordByte(c) || c == '-' {
			end = q + 1
		} else if c != '.' && c != '/' {
			break
		}
	}
	return end
}

// matchInt is /0|[1-9][0-9]*(?!\.)/y. The lookahead binds to the second branch only, which
// gives back digits until no dot follows.
func matchInt(src string, p int) int {
	if p >= len(src) {
		return -1
	}
	if src[p] == '0' {
		return p + 1
	}
	if src[p] < '1' || src[p] > '9' {
		return -1
	}
	q := p + 1
	for q < len(src) && src[q] >= '0' && src[q] <= '9' {
		q++
	}
	if q < len(src) && src[q] == '.' {
		if q-1 > p {
			return q - 1
		}
		return -1
	}
	return q
}

// openAccDescr is /[\t ]*accDescr\s*{/y.
func openAccDescr(src string, p int) int {
	q := p
	for q < len(src) && (src[q] == ' ' || src[q] == '\t') {
		q++
	}
	if !strings.HasPrefix(src[q:], "accDescr") {
		return -1
	}
	q += len("accDescr")
	for q < len(src) {
		c := rune(src[q])
		size := 1
		if src[q] >= 0x80 {
			c, size = utf8.DecodeRuneInString(src[q:])
		}
		if !util.IsSpace(c) {
			break
		}
		q += size
	}
	if q < len(src) && src[q] == '{' {
		return q + 1
	}
	return -1
}

var REFERENCE = &common.TokenType{Name: "REFERENCE", Match: matchReference, First: "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"}
var NAME = []*common.TokenType{REFERENCE}

// word: a keyword that is the start of a longer name is part of that name: `commits` is a branch.
func word(name string) *common.TokenType {
	return &common.TokenType{Name: name, Pattern: name, Longer: NAME}
}

func with(t *common.TokenType, change func(*common.TokenType)) *common.TokenType {
	c := *t
	change(&c)
	return &c
}

var GIT_TOKENS = []*common.TokenType{
	word("cherry-pick"),
	{Name: "gitGraph:", Pattern: "gitGraph:"},
	word("HIGHLIGHT"),
	with(common.Keyword("gitGraph"), func(t *common.TokenType) { t.Longer = NAME }),
	word("checkout"),
	word("REVERSE"),
	{Name: "parent:", Pattern: "parent:"},
	word("commit"),
	word("NORMAL"),
	word("branch"),
	{Name: "order:", Pattern: "order:"},
	word("switch"),
	{Name: "type:", Pattern: "type:"},
	word("merge"),
	{Name: "msg:", Pattern: "msg:"},
	{Name: "tag:", Pattern: "tag:"},
	{Name: "id:", Pattern: "id:"},
	word("LR"),
	word("TB"),
	word("BT"),
	{Name: ":", Pattern: ":"},
	// Without a closing brace the block form scans to the end of the text, and a brace is not a
	// token on its own, so the first failure is final.
	with(common.ACC_DESCR, func(t *common.TokenType) { t.Opener = openAccDescr }),
	common.ACC_TITLE,
	common.TitleBefore(common.RunEndsIn("-./", "-")),
	{Name: "INT", Match: matchInt, First: "0123456789"},
	common.STRING,
	common.NEWLINE,
	common.WHITESPACE,
	common.YAML,
	common.DIRECTIVE,
	common.SINGLE_LINE_COMMENT,
	REFERENCE,
}

type CommitTypeName = string

// StatementAst is one of Commit, Branch, Merge, Checkout and CherryPicking, named by typ.
// An id, message or parent that is not given is "": pele reads each of them as text that
// is empty when it is missing.
type StatementAst struct {
	typ     string
	id      string
	message string
	tags    []string
	// commitType is "" for none.
	commitType CommitTypeName
	name       string
	order      *float64
	branch     string
	parent     string
}

// GitGraphAst: Langium names the root after the `Direction` rule when the header gives a direction.
type GitGraphAst struct {
	typ        string
	dir        string
	title      string
	accTitle   string
	accDescr   string
	statements []StatementAst
}

func parseGit(src string) *GitGraphAst {
	r := common.NewReader(common.Tokenize(src, GIT_TOKENS, "gitGraph"), GIT_TOKENS, "gitGraph")
	ast := &GitGraphAst{typ: "GitGraph"}

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
	name := func() string {
		if r.Kind() == T_reference {
			return r.Take()
		}
		if r.Kind() == T_string {
			return common.StringValue(r.Take())
		}
		r.Fail("a branch name")
		return ""
	}
	str := func() string { return common.StringValue(r.Expect(T_string)) }
	commitType := func() CommitTypeName {
		if r.Kind() != T_normal && r.Kind() != T_reverse && r.Kind() != T_highlight {
			r.Fail("NORMAL, REVERSE, or HIGHLIGHT")
		}
		return r.Take()
	}

	for r.Kind() == T_newline {
		r.I++
	}
	if !r.Accept(T_gitGraphColon) {
		r.Expect(T_gitGraph)
		next := -1
		if r.I+1 < len(r.Kinds) {
			next = r.Kinds[r.I+1]
		}
		if (r.Kind() == T_lr || r.Kind() == T_tb || r.Kind() == T_bt) && next == T_colon {
			ast.typ = "Direction"
			ast.dir = r.Take()
			r.I++
		} else {
			r.Accept(T_colon)
		}
	}

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
		case T_commit:
			r.I++
			node := StatementAst{typ: "Commit", tags: []string{}}
			for {
				if r.Accept(T_id) {
					node.id = str()
				} else if r.Accept(T_tag) {
					node.tags = append(node.tags, str())
				} else if r.Accept(T_type) {
					node.commitType = commitType()
				} else if r.Accept(T_msg) || r.Kind() == T_string {
					node.message = str()
				} else {
					break
				}
			}
			endOfLine()
			ast.statements = append(ast.statements, node)
		case T_branch:
			r.I++
			node := StatementAst{typ: "Branch", name: name()}
			if r.Accept(T_order) {
				// parseInt of a run of digits.
				order := js.Number(r.Expect(T_int))
				node.order = &order
			}
			endOfLine()
			ast.statements = append(ast.statements, node)
		case T_merge:
			r.I++
			node := StatementAst{typ: "Merge", branch: name(), tags: []string{}}
			for {
				if r.Accept(T_id) {
					node.id = str()
				} else if r.Accept(T_tag) {
					node.tags = append(node.tags, str())
				} else if r.Accept(T_type) {
					node.commitType = commitType()
				} else {
					break
				}
			}
			endOfLine()
			ast.statements = append(ast.statements, node)
		case T_checkout, T_switch:
			r.I++
			node := StatementAst{typ: "Checkout", branch: name()}
			endOfLine()
			ast.statements = append(ast.statements, node)
		case T_cherryPick:
			r.I++
			node := StatementAst{typ: "CherryPicking", tags: []string{}}
			for {
				if r.Accept(T_id) {
					node.id = str()
				} else if r.Accept(T_tag) {
					node.tags = append(node.tags, str())
				} else if r.Accept(T_parent) {
					node.parent = str()
				} else {
					break
				}
			}
			endOfLine()
			ast.statements = append(ast.statements, node)
		default:
			r.Fail("a statement, a title, or a line break")
		}
	}
	return ast
}
