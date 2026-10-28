// Ported from pele src/diagrams/packet/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package packet

import (
	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

const (
	T_packetBeta = iota
	T_packet
	T_dash
	T_plus
	T_colon
	T_accDescr
	T_accTitle
	T_title
	T_int
	T_string
	T_newline
)

// matchInt answers as /0|[1-9][0-9]*(?!\.)/y does: a run of digits gives back its last digit
// when a dot follows it.
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
	e := p + 1
	for e < len(src) && src[e] >= '0' && src[e] <= '9' {
		e++
	}
	if e == len(src) || src[e] != '.' {
		return e
	}
	if e-1 > p {
		return e - 1
	}
	return -1
}

var PACKET_TOKENS = []*common.TokenType{
	{Name: "packet-beta", Pattern: "packet-beta"},
	common.Keyword("packet"),
	// A front matter block also starts with dashes, so Langium lets it win over a dash.
	{Name: "-", Pattern: "-", Longer: []*common.TokenType{common.YAML}},
	{Name: "+", Pattern: "+"},
	{Name: ":", Pattern: ":"},
	common.ACC_DESCR,
	common.ACC_TITLE,
	common.TITLE,
	{Name: "INT", Match: matchInt, First: "0123456789"},
	common.STRING,
	common.NEWLINE,
	common.WHITESPACE,
	common.YAML,
	common.DIRECTIVE,
	common.SINGLE_LINE_COMMENT,
}

// PacketBlockAst: start, end and bits are NaN when not given.
type PacketBlockAst struct {
	start, end, bits float64
	hasStart         bool
	hasEnd           bool
	hasBits          bool
	label            string
}

type PacketAst struct {
	title    string
	accTitle string
	accDescr string
	blocks   []PacketBlockAst
}

func parsePacket(src string) *PacketAst {
	r := common.NewReader(common.Tokenize(src, PACKET_TOKENS, "packet"), PACKET_TOKENS, "packet")
	ast := &PacketAst{}

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
	if !r.Accept(T_packetBeta) {
		r.Expect(T_packet)
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
		case T_int, T_plus:
			var block PacketBlockAst
			if r.Accept(T_plus) {
				block.bits, block.hasBits = js.Number(r.Expect(T_int)), true
			} else {
				block.start, block.hasStart = js.Number(r.Take()), true
				if r.Accept(T_dash) {
					block.end, block.hasEnd = js.Number(r.Expect(T_int)), true
				}
			}
			r.Expect(T_colon)
			block.label = common.StringValue(r.Expect(T_string))
			ast.blocks = append(ast.blocks, block)
			endOfLine()
		default:
			r.Fail("a field, a title, or a line break")
		}
	}
	return ast
}
