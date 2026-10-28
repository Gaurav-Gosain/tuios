// Ported from pele src/diagrams/quadrant/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package quadrant

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
	WORD = flags(T_PUNCTUATION, T_AMP, T_NUM, T_ALPHA, T_COMMA, T_PLUS, T_EQUALS, T_MULT, T_DOT, T_BRKT, T_UNDERSCORE,
		T_UNICODE_TEXT)
	ID    = flags(T_ALPHA, T_NUM, T_MINUS, T_COMMA, T_COLON, T_AMP, T_BRKT, T_MULT, T_UNICODE_TEXT)
	STYLE = flags(T_ALPHA, T_NUM, T_COLON, T_SPACE, T_BRKT, T_MINUS)
	EOL   = flags(T_NEWLINE, T_SEMI, T_EOF)
)

const TEXT_TOKENS = "'STR', 'MD_STR', 'ALPHA', 'NUM', 'UNICODE_TEXT', 'PUNCTUATION'"

// in reports whether t is set in a flag table; -1, past the end of the tokens, is in none.
func in(table *[T_EOF + 1]bool, t int) bool { return t >= 0 && table[t] }

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

func (p *parser) take() string {
	p.i++
	return p.toks.texts[p.i-1]
}

// join returns the texts of the tokens from start up to the current one, in one allocation.
func (p *parser) join(start int) string {
	return strings.Join(p.toks.texts[start:p.i], "")
}

func (p *parser) fail(expected string) {
	t := p.typ(p.i)
	if t == T_ERROR {
		panic(types.SyntaxError("quadrantChart", p.src, p.toks.starts[p.i], "", true))
	}
	name := "undefined"
	start := len(p.src)
	if t >= 0 {
		name = TOKEN_NAMES[t]
		start = p.toks.starts[p.i]
	}
	panic(types.SyntaxError("quadrantChart", p.src, start, "Expecting "+expected+", got '"+name+"'", false))
}

func (p *parser) expect(typ int) string {
	if p.typ(p.i) != typ {
		p.fail("'" + TOKEN_NAMES[typ] + "'")
	}
	return p.take()
}

func isText(t int) bool { return t == T_STR || t == T_MD_STR || in(&WORD, t) }

func (p *parser) text() QuadrantText {
	first := p.typ(p.i)
	if !isText(first) {
		p.fail(TEXT_TOKENS)
	}
	start := p.i
	p.i++
	for t := p.typ(p.i); in(&WORD, t) || t == T_SPACE || t == T_MINUS; t = p.typ(p.i) {
		p.i++
	}
	// One join, not a += per token: Go copies the string on each +=, where JavaScript ropes do not.
	s := p.join(start)
	typ := "text"
	if first == T_MD_STR {
		typ = "markdown"
	}
	return QuadrantText{text: s, typ: typ}
}

func (p *parser) styles() []string {
	var out []string
	for {
		if !in(&STYLE, p.typ(p.i)) {
			p.fail("'ALPHA', 'NUM', 'COLON', 'SPACE', 'BRKT', 'MINUS'")
		}
		start := p.i
		p.i++
		for in(&STYLE, p.typ(p.i)) {
			p.i++
		}
		out = append(out, js.Trim(p.join(start)))
		if p.typ(p.i) != T_COMMA {
			return out
		}
		p.i++
	}
}

func parseQuadrant(src string, db *QuadrantDb) {
	p := &parser{src: src, toks: tokenize(src)}

	for in(&EOL, p.typ(p.i)) || p.typ(p.i) == T_SPACE {
		p.i++
	}
	p.expect(T_QUADRANT)

	for p.typ(p.i) != T_END {
		for p.typ(p.i) == T_SPACE {
			p.i++
		}
		t := p.typ(p.i)
		switch t {
		case T_CLASSDEF:
			p.i++
			p.expect(T_SPACE)
			if !in(&ID, p.typ(p.i)) {
				p.fail("'ALPHA', 'NUM', 'UNICODE_TEXT'")
			}
			start := p.i
			p.i++
			for in(&ID, p.typ(p.i)) {
				p.i++
			}
			id := p.join(start)
			p.expect(T_SPACE)
			db.addClass(id, p.styles())
		case T_X_AXIS, T_Y_AXIS:
			p.i++
			from := p.text()
			var to *QuadrantText
			if p.typ(p.i) == T_DELIMITER {
				p.i++
				if isText(p.typ(p.i)) {
					text := p.text()
					to = &text
				} else {
					from.text += " ⟶ "
				}
			}
			if t == T_X_AXIS {
				db.setXAxisLeftText(from)
				if to != nil {
					db.setXAxisRightText(*to)
				}
			} else {
				db.setYAxisBottomText(from)
				if to != nil {
					db.setYAxisTopText(*to)
				}
			}
		case T_QUADRANT_1, T_QUADRANT_2, T_QUADRANT_3, T_QUADRANT_4:
			p.i++
			db.setQuadrantText(t-T_QUADRANT_1, p.text())
		case T_title:
			p.i++
			db.setDiagramTitle(js.Trim(p.expect(T_title_value)))
		case T_acc_title:
			p.i++
			db.setAccTitle(js.Trim(p.expect(T_acc_title_value)))
		case T_acc_descr:
			p.i++
			db.setAccDescription(js.Trim(p.expect(T_acc_descr_value)))
		case T_acc_descr_multiline_value:
			db.setAccDescription(js.Trim(p.take()))
		default:
			if isText(t) {
				label := p.text()
				className := ""
				if p.typ(p.i) == T_class_name {
					className = p.take()
				}
				p.expect(T_point_start)
				x := p.expect(T_point_x)
				y := p.expect(T_point_y)
				var styles []string
				if in(&STYLE, p.typ(p.i)) {
					styles = p.styles()
				}
				db.addPoint(label, className, x, y, styles)
			}
		}
		if !in(&EOL, p.typ(p.i)) {
			p.fail("'NEWLINE', 'SEMI', 'EOF'")
		}
		p.i++
	}
}
