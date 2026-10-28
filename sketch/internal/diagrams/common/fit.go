// Ported from pele src/diagrams/common/fit.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// The three regular expressions are hand matchers here: RE_TAG_START has a lookahead,
// and RE_UNIT cuts text into UTF-16 units, which a rune of Go stands for.

package common

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/text"
)

// brAt is the end of `<br\s*\/?>` at p, ignoring case, or -1.
func brAt(s string, p int) int {
	if !(p+3 <= len(s) && s[p] == '<' && s[p+1]|32 == 'b' && s[p+2]|32 == 'r') {
		return -1
	}
	q := p + 3
	for q < len(s) {
		r, size := utf8.DecodeRuneInString(s[q:])
		if !js.IsSpace(r) {
			break
		}
		q += size
	}
	if q < len(s) && s[q] == '/' {
		q++
	}
	if q < len(s) && s[q] == '>' {
		return q + 1
	}
	return -1
}

// text.replace(/<(?!br\s*\/?>)/gi, '&lt;')
func escapeTagStarts(s string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '<' && brAt(s, i) == -1 {
			b.WriteString(s[last:i])
			b.WriteString("&lt;")
			last = i + 1
		}
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func wordRun(s string, p int) int {
	for p < len(s) && isWordByte(s[p]) {
		p++
	}
	return p
}

// unitAt is the end of the RE_UNIT match at p:
// /<br\s*\/?>|ﬂ°°?\w+¶ß|&#?\w+;|[\ud800-\udbff][\udc00-\udfff]|[\s\S]/gi.
// A line break, an encoded entity, a surrogate pair, or one character: the pieces a word can be cut between.
func unitAt(s string, p int) int {
	if e := brAt(s, p); e != -1 {
		return e
	}
	if strings.HasPrefix(s[p:], "ﬂ°") {
		q := p + len("ﬂ°")
		if strings.HasPrefix(s[q:], "°") {
			// °? is greedy, and gives the ° back when \w+ cannot follow it.
			if e := wordRun(s, q+len("°")); e > q+len("°") && strings.HasPrefix(s[e:], "¶ß") {
				return e + len("¶ß")
			}
		}
		if e := wordRun(s, q); e > q && strings.HasPrefix(s[e:], "¶ß") {
			return e + len("¶ß")
		}
	}
	if s[p] == '&' {
		q := p + 1
		if q < len(s) && s[q] == '#' {
			if e := wordRun(s, q+1); e > q+1 && e < len(s) && s[e] == ';' {
				return e + 1
			}
		}
		if e := wordRun(s, q); e > q && e < len(s) && s[e] == ';' {
			return e + 1
		}
	}
	_, size := utf8.DecodeRuneInString(s[p:])
	return p + size
}

// PlainLabel lays out text the way Mermaid's journey and timeline draw it: as plain text in which only
// <br> is markup, so other tags and icon names show as written instead of being read as formatting.
func PlainLabel(raw string, measurer text.TextMeasurer, size, max float64, base int) text.Label {
	t := raw
	if strings.Contains(t, "<") {
		t = escapeTagStarts(t)
	}
	if strings.Contains(t, ":fa-") {
		t = strings.ReplaceAll(t, ":fa-", "&colon;fa-")
	}
	return text.LayoutLabel(t, false, measurer, size, max, base)
}

// FitLabel is PlainLabel that also cuts a word too long to fit: layoutLabel wraps at spaces only,
// and a box of fixed width needs every line to fit.
func FitLabel(raw string, measurer text.TextMeasurer, size, max float64, base int) text.Label {
	label := PlainLabel(raw, measurer, size, max, base)
	if raw == "" || label.Width <= max {
		return label
	}
	var out strings.Builder
	width := 0.0
	for p := 0; p < len(raw); {
		e := unitAt(raw, p)
		unit := raw[p:e]
		p = e
		if js.Trim(unit) == "" || (js.Len(unit) > 2 && unit[0] == '<') {
			width = 0
		} else {
			measured := unit
			if js.Len(unit) > 2 {
				measured = "x"
			}
			w := measurer.Width(measured, size, base)
			if width > 0 && width+w > max {
				out.WriteByte('\n')
				width = 0
			}
			width += w
		}
		out.WriteString(unit)
	}
	return PlainLabel(out.String(), measurer, size, max, base)
}
