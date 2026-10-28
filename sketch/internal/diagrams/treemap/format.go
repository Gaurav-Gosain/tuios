// Ported from pele src/diagrams/treemap/format.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// RE_SPEC is a hand matcher: every part after the fill and alignment is optional and
// greedy, and none can start with what the part after it takes, so only the fill and
// alignment need trying in more than one way.

package treemap

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

// spec holds the groups of RE_SPEC:
// /^(?:.?[<>=^])?([-+( ])?(\$)?0?\d*(,)?(?:\.(\d+))?(~)?([a-zA-Z%])?$/.
type spec struct {
	sign      string
	dollar    bool
	comma     bool
	precision string
	hasPrec   bool
	trim      bool
	typ       string
}

func isAlign(c byte) bool { return c == '<' || c == '>' || c == '=' || c == '^' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// specFrom matches what follows the fill and alignment, to the end.
func specFrom(s string, p int) (spec, bool) {
	var m spec
	if p < len(s) && strings.IndexByte("-+( ", s[p]) >= 0 {
		m.sign = s[p : p+1]
		p++
	}
	if p < len(s) && s[p] == '$' {
		m.dollar = true
		p++
	}
	if p < len(s) && s[p] == '0' {
		p++
	}
	for p < len(s) && isDigit(s[p]) {
		p++
	}
	if p < len(s) && s[p] == ',' {
		m.comma = true
		p++
	}
	if p+1 < len(s) && s[p] == '.' && isDigit(s[p+1]) {
		q := p + 1
		for q < len(s) && isDigit(s[q]) {
			q++
		}
		m.precision, m.hasPrec = s[p+1:q], true
		p = q
	}
	if p < len(s) && s[p] == '~' {
		m.trim = true
		p++
	}
	if p < len(s) && (s[p] == '%' || s[p]|32 >= 'a' && s[p]|32 <= 'z') {
		m.typ = s[p : p+1]
		p++
	}
	return m, p == len(s)
}

func matchSpec(s string) (spec, bool) {
	// .?[<>=^]: `.` is one UTF-16 unit that is not a line end.
	if r, size := utf8.DecodeRuneInString(s); len(s) > 0 && r <= 0xffff && r != '\n' && r != '\r' && r != 0x2028 && r != 0x2029 &&
		size < len(s) && isAlign(s[size]) {
		if m, ok := specFrom(s, size+1); ok {
			return m, true
		}
	}
	if len(s) > 0 && isAlign(s[0]) {
		if m, ok := specFrom(s, 1); ok {
			return m, true
		}
	}
	return specFrom(s, 0)
}

func group(digits string) string {
	out := ""
	for i := len(digits); i > 0; i -= 3 {
		piece := digits[max(0, i-3):i]
		if out != "" {
			out = piece + "," + out
		} else {
			out = piece
		}
	}
	return out
}

// trimZeros drops the zeros at the end of body, and a point before them: /\.?0+$/ replaced by nothing.
func trimZeros(body string) string {
	end := len(body)
	for end > 0 && body[end-1] == '0' {
		end--
	}
	if end == len(body) {
		return body
	}
	if end > 0 && body[end-1] == '.' {
		end--
	}
	return body[:end]
}

func formatter(specText string) func(value float64) string {
	// Mermaid documents `$0,0` for a dollar amount with thousands separators.
	if specText == "$0,0" {
		specText = "$,"
	}
	m, ok := matchSpec(specText)
	if !ok {
		m, _ = matchSpec(",")
	}
	plus := m.sign == "+"
	precision := -1
	if m.hasPrec {
		precision = int(math.Min(js.Number(m.precision), 20))
	}
	or := func(fallback int) int {
		if precision < 0 {
			return fallback
		}
		return precision
	}

	return func(value float64) string {
		abs := math.Abs(value)
		var body string
		suffix := ""
		switch m.typ {
		case "f":
			body = js.ToFixed(abs, or(6))
		case "%":
			body = js.ToFixed(abs*100, or(6))
			suffix = "%"
		case "d":
			body = js.ToFixed(js.Round(abs), 0)
		case "e":
			body = js.ToExponential(abs, or(6))
		default:
			body = js.Num(js.PrecisionNumber(abs, max(1, or(12))))
		}

		if m.trim && strings.Contains(body, ".") && !strings.Contains(body, "e") {
			body = trimZeros(body)
		}
		if m.comma && !strings.Contains(body, "e") {
			if dot := strings.IndexByte(body, '.'); dot == -1 {
				body = group(body)
			} else {
				body = group(body[:dot]) + body[dot:]
			}
		}
		sign := ""
		if value < 0 {
			sign = "-"
		} else if plus {
			sign = "+"
		}
		if m.dollar {
			sign += "$"
		}
		return sign + body + suffix
	}
}
