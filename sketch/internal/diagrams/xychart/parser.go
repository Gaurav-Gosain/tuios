// Ported from pele src/diagrams/xychart/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package xychart

import (
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

func flags(tokens ...int) (out [T_EOF + 1]bool) {
	for _, t := range tokens {
		out[t] = true
	}
	return
}

var (
	WORD = flags(T_AMP, T_NUM, T_ALPHA, T_PLUS, T_EQUALS, T_MULT, T_DOT, T_BRKT, T_MINUS, T_UNDERSCORE)
	EOL  = flags(T_NEWLINE, T_SEMI, T_EOF)
)

type parser struct {
	src  string
	toks Tokens
	i    int
}

// typ is types[i], or -1 past the end, which pele reads as undefined.
func (p *parser) typ(i int) int {
	if i < 0 || i >= len(p.toks.types) {
		return -1
	}
	return p.toks.types[i]
}

func (p *parser) word(i int) bool {
	t := p.typ(i)
	return t >= 0 && WORD[t]
}

func (p *parser) eol(i int) bool {
	t := p.typ(i)
	return t >= 0 && EOL[t]
}

func (p *parser) fail(expected string) {
	t := p.typ(p.i)
	if t == T_ERROR {
		panic(types.SyntaxError("xychart", p.src, p.toks.starts[p.i], "", true))
	}
	name := "undefined"
	start := len(p.src)
	if t >= 0 {
		name = TOKEN_NAMES[t]
		start = p.toks.starts[p.i]
	}
	panic(types.SyntaxError("xychart", p.src, start, "Expecting "+expected+", got '"+name+"'", false))
}

func (p *parser) expect(typ int) string {
	if p.typ(p.i) != typ {
		p.fail("'" + TOKEN_NAMES[typ] + "'")
	}
	p.i++
	return p.toks.texts[p.i-1]
}

func (p *parser) isText(t int) bool { return t == T_STR || (t >= 0 && WORD[t]) }

func (p *parser) text() XyText {
	if !p.isText(p.typ(p.i)) {
		p.fail("'STR', 'ALPHA', 'NUM'")
	}
	start := p.i
	p.i++
	if p.typ(p.i-1) != T_STR {
		for p.word(p.i) {
			p.i++
		}
	}
	// One join, not a += per token: Go copies the string on each +=, where JavaScript ropes do not.
	return XyText{text: strings.Join(p.toks.texts[start:p.i], ""), typ: "text"}
}

func (p *parser) rng() (float64, float64) {
	min := js.Number(p.expect(T_NUMBER_WITH_DECIMAL))
	p.expect(T_ARROW_DELIMITER)
	return min, js.Number(p.expect(T_NUMBER_WITH_DECIMAL))
}

func (p *parser) points() []DataPoint {
	var out []DataPoint
	p.expect(T_SQUARE_BRACES_START)
	for {
		value := js.Number(p.expect(T_NUMBER_WITH_DECIMAL))
		label := ""
		if p.typ(p.i) == T_STR {
			label = p.toks.texts[p.i]
			p.i++
		}
		out = append(out, DataPoint{value, label})
		if p.typ(p.i) != T_COMMA {
			break
		}
		p.i++
	}
	p.expect(T_SQUARE_BRACES_END)
	return out
}

func parseXyChart(src string, db *XyChartDb) {
	p := &parser{src: src, toks: tokenize(src)}

	for {
		if p.eol(p.i) {
			p.i++
		} else if p.typ(p.i) != T_XYCHART {
			break
		} else if p.i++; p.typ(p.i) == T_CHART_ORIENTATION {
			db.setOrientation(p.toks.texts[p.i])
			p.i++
		}
	}

	for p.typ(p.i) != T_END {
		t := p.typ(p.i)
		p.i++
		switch t {
		case T_title:
			db.setDiagramTitle(js.Trim(p.text().text))
		case T_X_AXIS, T_Y_AXIS:
			var title *XyText
			if p.isText(p.typ(p.i)) {
				text := p.text()
				title = &text
			}
			if t == T_X_AXIS && p.typ(p.i) == T_SQUARE_BRACES_START {
				var categories []XyText
				for {
					p.i++
					categories = append(categories, p.text())
					if p.typ(p.i) != T_COMMA {
						break
					}
				}
				p.expect(T_SQUARE_BRACES_END)
				db.setXAxisBand(categories)
			} else if p.typ(p.i) == T_NUMBER_WITH_DECIMAL || title == nil {
				min, max := p.rng()
				if t == T_X_AXIS {
					db.setXAxisRangeData(min, max)
				} else {
					db.setYAxisRangeData(min, max)
				}
			}
			if title == nil {
				title = &XyText{typ: "text"}
			}
			if t == T_X_AXIS {
				db.setXAxisTitle(*title)
			} else {
				db.setYAxisTitle(*title)
			}
		case T_LINE, T_BAR:
			title := XyText{typ: "text"}
			if p.typ(p.i) != T_SQUARE_BRACES_START {
				title = p.text()
			}
			data := p.points()
			if t == T_LINE {
				db.setLineData(title, data)
			} else {
				db.setBarData(title, data)
			}
		case T_acc_title:
			db.setAccTitle(js.Trim(p.expect(T_acc_title_value)))
		case T_acc_descr:
			db.setAccDescription(js.Trim(p.expect(T_acc_descr_value)))
		case T_acc_descr_multiline_value:
			db.setAccDescription(js.Trim(p.toks.texts[p.i-1]))
		default:
			p.i--
			p.fail("'title', 'X_AXIS', 'Y_AXIS', 'LINE', 'BAR', 'acc_title', 'acc_descr', 'acc_descr_multiline_value'")
		}
		for p.eol(p.i) {
			p.i++
		}
	}
}
