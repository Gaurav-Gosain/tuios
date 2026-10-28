// Ported from pele src/diagrams/wardley/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package wardley

import (
	"strings"
	"sync"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/util"
)

const (
	T_lbracket = iota
	T_comma
	T_rbracket
	T_at
	T_slash
	T_lparen
	T_rparen
	T_minus
	T_lbrace
	T_rbrace
	T_number
	T_arrow
	T_linkPort
	T_linkArrow
	T_linkLabel
	T_strategy
	T_wardley
	T_size
	T_evolution
	T_anchor
	T_component
	T_label
	T_inertia
	T_evolve
	T_pipeline
	T_note
	T_annotations
	T_annotation
	T_accelerator
	T_deaccelerator
	T_name
	T_ws
	T_accDescr
	T_accTitle
	T_title
	T_int
	T_string
	T_id
	T_newline
)

func digits(src string, from int) int {
	i := from
	for i < len(src) && src[i] >= '0' && src[i] <= '9' {
		i++
	}
	return i
}

// numberScanner answers as WARDLEY_NUMBER's pattern does. The pattern reads a whole run of digits before it
// finds there is no fraction, and would do so again from every digit of a long run.
func numberScanner() common.Scanner {
	from, end := 0, 0
	return func(src string, p int) int {
		if p < from || p >= end {
			from = p
			end = digits(src, p)
		}
		if end == p || end >= len(src) || src[end] != '.' {
			return -1
		}
		stop := digits(src, end+1)
		if stop > end+1 {
			return stop
		}
		return -1
	}
}

// /->/y
func matchArrow(src string, p int) int {
	if strings.HasPrefix(src[p:], "->") {
		return p + 2
	}
	return -1
}

// /-->|-\.->|>|\+'[^']*'<>|\+'[^']*'<|\+'[^']*'>/y
func matchLinkArrow(src string, p int) int {
	rest := src[p:]
	switch {
	case strings.HasPrefix(rest, "-->"):
		return p + 3
	case strings.HasPrefix(rest, "-.->"):
		return p + 4
	case strings.HasPrefix(rest, ">"):
		return p + 1
	case strings.HasPrefix(rest, "+'"):
		close := strings.IndexByte(rest[2:], '\'')
		if close < 0 {
			return -1
		}
		q := p + 2 + close + 1
		switch {
		case strings.HasPrefix(src[q:], "<>"):
			return q + 2
		case strings.HasPrefix(src[q:], "<"), strings.HasPrefix(src[q:], ">"):
			return q + 1
		}
	}
	return -1
}

// /\+<>|\+>|\+</y
func matchLinkPort(src string, p int) int {
	rest := src[p:]
	switch {
	case strings.HasPrefix(rest, "+<>"):
		return p + 3
	case strings.HasPrefix(rest, "+>"), strings.HasPrefix(rest, "+<"):
		return p + 2
	}
	return -1
}

// /;[^\n\r]+/y
func matchLinkLabel(src string, p int) int {
	if src[p] != ';' {
		return -1
	}
	q := p + 1
	for q < len(src) && src[q] != '\n' && src[q] != '\r' {
		q++
	}
	if q == p+1 {
		return -1
	}
	return q
}

func either(words ...string) common.Scanner {
	return func(src string, p int) int {
		for _, w := range words {
			if strings.HasPrefix(src[p:], w) {
				return p + len(w)
			}
		}
		return -1
	}
}

func isLetter(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }

// nameRun is (?:[A-Za-z0-9_()&]|-(?!>))* from q.
func nameRun(src string, q int) int {
	for q < len(src) {
		c := src[q]
		if isLetter(c) || (c >= '0' && c <= '9') || c == '_' || c == '(' || c == ')' || c == '&' {
			q++
		} else if c == '-' && (q+1 >= len(src) || src[q+1] != '>') {
			q++
		} else {
			break
		}
	}
	return q
}

// matchName answers as
// /(?!title\s|accTitle|accDescr)[A-Za-z](?:[A-Za-z0-9_()&]|-(?!>))*(?:[ \t]+[A-Za-z(](?:[A-Za-z0-9_()&]|-(?!>))*)*/y.
func matchName(src string, p int) int {
	rest := src[p:]
	if strings.HasPrefix(rest, "accTitle") || strings.HasPrefix(rest, "accDescr") {
		return -1
	}
	if strings.HasPrefix(rest, "title") && len(rest) > 5 {
		if c, _ := utf8.DecodeRuneInString(rest[5:]); js.IsSpace(c) {
			return -1
		}
	}
	if !isLetter(src[p]) {
		return -1
	}
	q := nameRun(src, p+1)
	for {
		b := q
		for b < len(src) && (src[b] == ' ' || src[b] == '\t') {
			b++
		}
		if b == q || b >= len(src) || !(isLetter(src[b]) || src[b] == '(') {
			return q
		}
		q = nameRun(src, b+1)
	}
}

// /0|[1-9][0-9]*(?!\.)/y: a run of digits gives back its last digit when a dot follows it.
func matchInt(src string, p int) int {
	if src[p] == '0' {
		return p + 1
	}
	if src[p] < '1' || src[p] > '9' {
		return -1
	}
	e := digits(src, p+1)
	if e == len(src) || src[e] != '.' {
		return e
	}
	if e-1 > p {
		return e - 1
	}
	return -1
}

// /[\w]([-\w]*\w)?/y: the run of word characters and dashes, up to its last word character.
func matchID(src string, p int) int {
	if !util.IsWord(rune(src[p])) {
		return -1
	}
	end := p + 1
	for q := p + 1; q < len(src) && (src[q] == '-' || util.IsWord(rune(src[q]))); q++ {
		if src[q] != '-' {
			end = q + 1
		}
	}
	return end
}

// /[ \t]+/y
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

var ARROW = &common.TokenType{Name: "ARROW", Match: matchArrow, First: "-"}
var LINK_ARROW = &common.TokenType{Name: "LINK_ARROW", Match: matchLinkArrow, First: "->+"}

func word(name, text string) *common.TokenType {
	return &common.TokenType{Name: name, Match: either(text), First: text[:1]}
}

var (
	tokensOnce     sync.Once
	WARDLEY_TOKENS []*common.TokenType
)

// wardleyTokens is the token table, made on the first parse so that a program that draws no
// wardley map pays nothing for it at start.
func wardleyTokens() []*common.TokenType {
	tokensOnce.Do(func() { WARDLEY_TOKENS = buildTokens() })
	return WARDLEY_TOKENS
}

func buildTokens() []*common.TokenType {
	return []*common.TokenType{
		{Name: "[", Pattern: "["},
		{Name: ",", Pattern: ","},
		{Name: "]", Pattern: "]"},
		{Name: "@", Pattern: "@"},
		{Name: "/", Pattern: "/"},
		{Name: "(", Pattern: "("},
		{Name: ")", Pattern: ")"},
		{Name: "-", Pattern: "-", Longer: []*common.TokenType{ARROW, LINK_ARROW, common.YAML}},
		{Name: "{", Pattern: "{"},
		{Name: "}", Pattern: "}"},
		{Name: "WARDLEY_NUMBER", First: "0123456789", Scanner: numberScanner},
		ARROW,
		{Name: "LINK_PORT", Match: matchLinkPort, First: "+"},
		LINK_ARROW,
		{Name: "LINK_LABEL", Match: matchLinkLabel, First: ";"},
		{Name: "STRATEGY", Match: either("build", "buy", "outsource", "market"), First: "bom"},
		word("KW_WARDLEY", "wardley-beta"),
		word("KW_SIZE", "size"),
		word("KW_EVOLUTION", "evolution"),
		word("KW_ANCHOR", "anchor"),
		word("KW_COMPONENT", "component"),
		word("KW_LABEL", "label"),
		word("KW_INERTIA", "inertia"),
		word("KW_EVOLVE", "evolve"),
		word("KW_PIPELINE", "pipeline"),
		word("KW_NOTE", "note"),
		word("KW_ANNOTATIONS", "annotations"),
		word("KW_ANNOTATION", "annotation"),
		word("KW_ACCELERATOR", "accelerator"),
		word("KW_DEACCELERATOR", "deaccelerator"),
		{Name: "NAME_WITH_SPACES", Match: matchName, First: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"},
		{Name: "WS", Match: matchBlanks, Hidden: true, First: "\t "},
		common.AccDescr(""),
		common.ACC_TITLE,
		common.TITLE,
		{Name: "INT", Match: matchInt, First: "0123456789"},
		common.STRING,
		{Name: "ID", Match: matchID, First: "_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"},
		common.NEWLINE,
		common.WHITESPACE,
		common.YAML,
		common.DIRECTIVE,
		common.SINGLE_LINE_COMMENT,
	}
}

type LabelAst struct {
	negX    bool
	offsetX float64
	negY    bool
	offsetY float64
}

type StageAst struct {
	name        string
	boundary    float64
	hasBoundary bool
	secondName  string
	hasSecond   bool
}

type ComponentAst struct {
	name                  string
	visibility, evolution float64
	label                 *LabelAst
	strategy              string
	inertia               bool
}

type PipelineComponentAst struct {
	name      string
	evolution float64
	label     *LabelAst
}

type LinkAst struct {
	from, fromPort, arrow, to, toPort string
	linkLabel                         string
	hasLabel                          bool
}

type placedAst struct {
	name string
	x, y float64
}

type WardleyAst struct {
	title        string
	accTitle     string
	accDescr     string
	size         *[2]float64
	stages       []StageAst
	hasEvolution bool
	anchors      []struct {
		name                  string
		visibility, evolution float64
	}
	components []ComponentAst
	links      []LinkAst
	evolves    []struct {
		component string
		target    float64
	}
	pipelines []struct {
		parent     string
		components []PipelineComponentAst
	}
	notes []struct {
		text                  string
		visibility, evolution float64
	}
	annotations []struct{ x, y float64 }
	annotation  []struct {
		number, x, y float64
		text         string
	}
	accelerators   []placedAst
	deaccelerators []placedAst
}

func parseWardley(src string) *WardleyAst {
	tokens := wardleyTokens()
	r := common.NewReader(common.Tokenize(src, tokens, "wardley"), tokens, "wardley")
	ast := &WardleyAst{}

	at := func(kind int) bool { return r.Kind() == kind }
	isName := func() bool { return at(T_string) || at(T_id) || at(T_name) }
	name := func() string {
		if at(T_string) {
			return common.StringValue(r.Take())
		}
		if at(T_id) || at(T_name) {
			return r.Take()
		}
		r.Fail("a name")
		return ""
	}
	number := func() float64 { return js.Number(r.Expect(T_number)) }
	parseInt := func(s string) float64 { return js.ParseIntPrefix(s, 10) }
	integer := func() float64 { return parseInt(r.Expect(T_int)) }
	coordinate := func() float64 {
		if at(T_number) {
			return js.Number(r.Take())
		}
		return parseInt(r.Expect(T_int))
	}
	endOfLine := func() {
		if at(-1) {
			return
		}
		if !at(T_newline) {
			r.Fail("a line break or the end of input")
		}
		for at(T_newline) {
			r.I++
		}
	}
	pair := func(read func() float64) (float64, float64) {
		r.Expect(T_lbracket)
		first := read()
		r.Expect(T_comma)
		second := read()
		r.Expect(T_rbracket)
		return first, second
	}
	label := func() *LabelAst {
		r.Expect(T_label)
		r.Expect(T_lbracket)
		out := &LabelAst{}
		out.negX = r.Accept(T_minus)
		out.offsetX = integer()
		r.Expect(T_comma)
		out.negY = r.Accept(T_minus)
		out.offsetY = integer()
		r.Expect(T_rbracket)
		return out
	}
	stage := func() StageAst {
		out := StageAst{name: name()}
		if r.Accept(T_at) {
			out.boundary, out.hasBoundary = number(), true
		}
		if r.Accept(T_slash) {
			out.secondName, out.hasSecond = name(), true
		}
		return out
	}

	for at(T_newline) {
		r.I++
	}
	r.Expect(T_wardley)
	for !at(-1) {
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
		case T_size:
			r.I++
			w, h := pair(integer)
			endOfLine()
			ast.size = &[2]float64{w, h}
		case T_evolution:
			r.I++
			stages := []StageAst{stage()}
			r.Expect(T_arrow)
			stages = append(stages, stage())
			for r.Accept(T_arrow) {
				stages = append(stages, stage())
			}
			endOfLine()
			ast.stages, ast.hasEvolution = stages, true
		case T_anchor:
			r.I++
			anchor := name()
			visibility, evolution := pair(number)
			endOfLine()
			ast.anchors = append(ast.anchors, struct {
				name                  string
				visibility, evolution float64
			}{anchor, visibility, evolution})
		case T_component:
			r.I++
			component := ComponentAst{name: name()}
			component.visibility, component.evolution = pair(number)
			if at(T_label) {
				component.label = label()
			}
			if at(T_lparen) && r.I+1 < len(r.Kinds) && r.Kinds[r.I+1] == T_strategy {
				r.I++
				component.strategy = r.Take()
				r.Expect(T_rparen)
			}
			if r.Accept(T_inertia) {
				component.inertia = true
			} else if r.Accept(T_lparen) {
				r.Expect(T_inertia)
				r.Expect(T_rparen)
				component.inertia = true
			}
			endOfLine()
			ast.components = append(ast.components, component)
		case T_evolve:
			r.I++
			component := name()
			target := number()
			endOfLine()
			ast.evolves = append(ast.evolves, struct {
				component string
				target    float64
			}{component, target})
		case T_pipeline:
			r.I++
			parent := name()
			r.Expect(T_lbrace)
			r.Expect(T_newline)
			for at(T_newline) {
				r.I++
			}
			var components []PipelineComponentAst
			for {
				r.Expect(T_component)
				component := PipelineComponentAst{name: name()}
				r.Expect(T_lbracket)
				component.evolution = number()
				r.Expect(T_rbracket)
				if at(T_label) {
					component.label = label()
				}
				endOfLine()
				components = append(components, component)
				if !at(T_component) {
					break
				}
			}
			r.Expect(T_rbrace)
			endOfLine()
			ast.pipelines = append(ast.pipelines, struct {
				parent     string
				components []PipelineComponentAst
			}{parent, components})
		case T_note:
			r.I++
			text := common.StringValue(r.Expect(T_string))
			visibility, evolution := pair(number)
			endOfLine()
			ast.notes = append(ast.notes, struct {
				text                  string
				visibility, evolution float64
			}{text, visibility, evolution})
		case T_annotations:
			r.I++
			x, y := pair(coordinate)
			endOfLine()
			ast.annotations = append(ast.annotations, struct{ x, y float64 }{x, y})
		case T_annotation:
			r.I++
			n := integer()
			r.Expect(T_comma)
			x, y := pair(coordinate)
			text := common.StringValue(r.Expect(T_string))
			endOfLine()
			ast.annotation = append(ast.annotation, struct {
				number, x, y float64
				text         string
			}{n, x, y, text})
		case T_accelerator, T_deaccelerator:
			slow := r.Kind() == T_deaccelerator
			r.I++
			force := name()
			x, y := pair(number)
			endOfLine()
			if slow {
				ast.deaccelerators = append(ast.deaccelerators, placedAst{force, x, y})
			} else {
				ast.accelerators = append(ast.accelerators, placedAst{force, x, y})
			}
		default:
			if !isName() {
				r.Fail("a statement")
			}
			link := LinkAst{from: name()}
			if at(T_linkPort) {
				link.fromPort = r.Take()
			}
			if at(T_linkArrow) || at(T_arrow) {
				link.arrow = r.Take()
			}
			link.to = name()
			if at(T_linkPort) {
				link.toPort = r.Take()
			}
			if at(T_linkLabel) {
				link.linkLabel, link.hasLabel = js.Trim(r.Take()[1:]), true
			}
			endOfLine()
			ast.links = append(ast.links, link)
		}
	}
	return ast
}
