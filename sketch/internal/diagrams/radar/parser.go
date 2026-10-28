// Ported from pele src/diagrams/radar/parser.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele keeps the state of matchNumber and matchAccDescr in module variables and resets them
// for each text. Here they are scanners, which the tokenizer makes once per text.

package radar

import (
	"strings"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/util"
)

const (
	T_radarColon = iota
	T_radar
	T_showLegend
	T_graticule
	T_curve
	T_ticks
	T_axis
	T_max
	T_min
	T_colon
	T_comma
	T_open
	T_close
	T_openBrace
	T_closeBrace
	T_graticuleValue
	T_boolean
	T_accDescr
	T_accTitle
	T_title
	T_number
	T_string
	T_id
	T_newline
)

func charCode(src string, i int) int {
	if i < 0 || i >= len(src) {
		return -1
	}
	return int(src[i])
}

func digits(src string, at int) int {
	end := at
	for c := charCode(src, end); c >= 48 && c <= 57; c = charCode(src, end) {
		end++
	}
	return end
}

// newNumberScanner answers as the NUMBER pattern /(?:[0-9]+\.[0-9]+(?!\.))|(?:0|[1-9][0-9]*(?!\.))/y
// does, without its backtracking. As a regular expression it reads to the end of a run of
// digits from every position in the run, which is quadratic for a long run of zeros.
func newNumberScanner() common.Scanner {
	runFrom, runEnd := 0, 0
	return func(src string, at int) int {
		if at < runFrom || at >= runEnd {
			runFrom = at
			runEnd = digits(src, at)
		}
		whole := runEnd
		if whole == at {
			return -1
		}
		dot := charCode(src, whole) == 46
		if dot {
			fraction := digits(src, whole+1)
			length := fraction - whole - 1
			if length > 0 && charCode(src, fraction) != 46 {
				return fraction
			}
			if length > 1 {
				return fraction - 1
			}
		}
		if charCode(src, at) == 48 {
			return at + 1
		}
		if !dot {
			return whole
		}
		if whole-1 > at {
			return whole - 1
		}
		return -1
	}
}

// blockAt is the end of /[\t ]*accDescr\s*{/y at at, or -1.
func blockAt(src string, at int) int {
	q := at
	for q < len(src) && (src[q] == '\t' || src[q] == ' ') {
		q++
	}
	if !strings.HasPrefix(src[q:], "accDescr") {
		return -1
	}
	q += len("accDescr")
	for q < len(src) {
		r := rune(src[q])
		size := 1
		if r >= 0x80 {
			r, size = decode(src[q:])
		}
		if !js.IsSpace(r) {
			break
		}
		q += size
	}
	if q < len(src) && src[q] == '{' {
		return q + 1
	}
	return -1
}

// newAccDescrScanner: in the other grammars an `accDescr {` that never closes ends the lexing.
// Here `accDescr` is also a name and `{` a token, so each unclosed one would read to the end of
// the text again.
func newAccDescrScanner() common.Scanner {
	lastBrace := -2
	return func(src string, at int) int {
		if end := blockAt(src, at); end >= 0 {
			if lastBrace == -2 {
				lastBrace = strings.LastIndexByte(src, '}')
			}
			if end > lastBrace {
				return -1
			}
		}
		return common.ACC_DESCR.Match(src, at)
	}
}

// matchID answers as /[\w]([-\w]*\w)?/y: the run of word characters and dashes, up to its last word character.
func matchID(src string, p int) int {
	if p >= len(src) || !util.IsWord(rune(src[p])) {
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

// either matches the first of words at p.
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

var nameRest = common.RunEndsIn("-", "")

// optionValue gives way to a longer name, except right after its option, as in `graticule polygonaxis`.
func optionValue(match common.Scanner, option int) func(src string, at, previous int) int {
	return func(src string, at, previous int) int {
		end := match(src, at)
		if end < 0 || previous == option {
			return end
		}
		if nameRest(src, end) {
			return -1
		}
		return end
	}
}

const wordChars = "-_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

var ID = &common.TokenType{Name: "ID", Match: matchID, First: wordChars[1:]}

func word(text string) *common.TokenType {
	return &common.TokenType{Name: text, Pattern: text, Longer: []*common.TokenType{ID}}
}

func mark(text string) *common.TokenType { return &common.TokenType{Name: text, Pattern: text} }

func radarKeyword() *common.TokenType {
	k := *common.Keyword("radar-beta")
	k.Longer = []*common.TokenType{ID}
	return &k
}

var RADAR_TOKENS = []*common.TokenType{
	mark("radar-beta:"),
	radarKeyword(),
	word("showLegend"),
	word("graticule"),
	word("curve"),
	word("ticks"),
	word("axis"),
	word("max"),
	word("min"),
	mark(":"),
	mark(","),
	mark("["),
	mark("]"),
	mark("{"),
	mark("}"),
	{Name: "GRATICULE", MatchAfter: optionValue(either("circle", "polygon"), T_graticule), First: "cp"},
	{Name: "BOOLEAN", MatchAfter: optionValue(either("true", "false"), T_showLegend), First: "tf"},
	{Name: common.ACC_DESCR.Name, Scanner: newAccDescrScanner, First: common.ACC_DESCR.First},
	common.ACC_TITLE,
	common.TitleBefore(common.RunEndsIn("-", "")),
	{Name: "NUMBER", Scanner: newNumberScanner, First: "0123456789"},
	common.STRING,
	ID,
	common.NEWLINE,
	common.WHITESPACE,
	common.YAML,
	common.DIRECTIVE,
	common.SINGLE_LINE_COMMENT,
}

type AxisAst struct {
	name     string
	label    string
	hasLabel bool
}

type EntryAst struct {
	// axis is the reference's text: Langium leaves the reference unresolved; Mermaid reads only its text.
	axis    string
	hasAxis bool
	value   float64
}

type CurveAst struct {
	name     string
	label    string
	hasLabel bool
	entries  []EntryAst
}

type OptionAst struct {
	name    string
	boolean bool
	number  float64
	text    string
}

type RadarAst struct {
	title    string
	accTitle string
	accDescr string
	axes     []AxisAst
	curves   []CurveAst
	options  []OptionAst
}

func parseRadar(src string) *RadarAst {
	r := common.NewReader(common.Tokenize(src, RADAR_TOKENS, "radar"), RADAR_TOKENS, "radar")
	ast := &RadarAst{}

	newlines := func() {
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
		newlines()
	}
	label := func() (string, bool) {
		if !r.Accept(T_open) {
			return "", false
		}
		text := common.StringValue(r.Expect(T_string))
		r.Expect(T_close)
		return text, true
	}
	entry := func(detailed bool) EntryAst {
		if !detailed {
			return EntryAst{value: js.Number(r.Expect(T_number))}
		}
		axis := r.Expect(T_id)
		r.Accept(T_colon)
		return EntryAst{axis: axis, hasAxis: true, value: js.Number(r.Expect(T_number))}
	}
	curve := func() CurveAst {
		node := CurveAst{name: r.Expect(T_id)}
		node.label, node.hasLabel = label()
		r.Expect(T_openBrace)
		newlines()
		if r.Kind() != T_number && r.Kind() != T_id {
			r.Fail("a number or an axis name")
		}
		detailed := r.Kind() == T_id
		node.entries = append(node.entries, entry(detailed))
		for r.Accept(T_comma) {
			newlines()
			node.entries = append(node.entries, entry(detailed))
		}
		newlines()
		r.Expect(T_closeBrace)
		return node
	}
	axis := func() AxisAst {
		node := AxisAst{name: r.Expect(T_id)}
		node.label, node.hasLabel = label()
		return node
	}
	option := func() OptionAst {
		kind := r.Kind()
		node := OptionAst{name: r.Take()}
		switch kind {
		case T_showLegend:
			node.boolean = r.Expect(T_boolean) == "true"
		case T_graticule:
			node.text = r.Expect(T_graticuleValue)
		default:
			node.number = js.Number(r.Expect(T_number))
		}
		return node
	}
	isOption := func() bool {
		k := r.Kind()
		return k == T_showLegend || k == T_ticks || k == T_max || k == T_min || k == T_graticule
	}

	newlines()
	if !r.Accept(T_radarColon) {
		r.Expect(T_radar)
		r.Accept(T_colon)
	}
	newlines()
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
		case T_axis:
			r.I++
			ast.axes = append(ast.axes, axis())
			for r.Accept(T_comma) {
				ast.axes = append(ast.axes, axis())
			}
		case T_curve:
			r.I++
			ast.curves = append(ast.curves, curve())
			for r.Accept(T_comma) {
				ast.curves = append(ast.curves, curve())
			}
		default:
			if !isOption() {
				r.Fail("an axis, a curve, an option, a title, or a line break")
			}
			ast.options = append(ast.options, option())
			for r.Accept(T_comma) {
				if !isOption() {
					r.Fail("an option")
				}
				ast.options = append(ast.options, option())
			}
		}
	}
	return ast
}
