// Ported from pele src/diagrams/sankey/parser.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package sankey

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

func isBlank(c rune) bool {
	if c == 10 || c == 13 {
		return false
	}
	if c < 128 {
		return c == 32 || c == 9 || c == 11 || c == 12
	}
	return c == 0xa0 || c == 0x1680 || (c >= 0x2000 && c <= 0x200a) || c == 0x2028 || c == 0x2029 ||
		c == 0x202f || c == 0x205f || c == 0x3000 || c == 0xfeff
}

// prepareTextForParsing is Mermaid's sankeyUtils: strip blanks at both ends of the text, then collapse
// runs of line breaks so that no row is empty. Its first step is a regex that rescans every run of
// blanks; this does the same in one pass.
func prepareTextForParsing(text string) string {
	start, end := 0, len(text)
	for start < end {
		r, size := utf8.DecodeRuneInString(text[start:end])
		if !isBlank(r) {
			break
		}
		start += size
	}
	for end > start {
		r, size := utf8.DecodeLastRuneInString(text[start:end])
		if !isBlank(r) {
			break
		}
		end -= size
	}
	return js.Trim(collapseBreaks(text[start:end]))
}

// collapseBreaks is .replace(/[\n\r]+/g, '\n').
func collapseBreaks(text string) string {
	if !strings.ContainsAny(text, "\r") && !strings.Contains(text, "\n\n") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != '\n' && c != '\r' {
			out.WriteByte(c)
			continue
		}
		for i+1 < len(text) && (text[i+1] == '\n' || text[i+1] == '\r') {
			i++
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// invalidXML is RE_INVALID_XML.test(text): /[\x00-\x08\x0b\x0c\x0e-\x1f\ud800-\udfff\ufffe\uffff]/u.
// Valid UTF-8 holds no surrogate, and DecodeEntities makes none.
func invalidXML(text string) bool {
	for _, r := range text {
		if r <= 0x08 || r == 0x0b || r == 0x0c || (r >= 0x0e && r <= 0x1f) || r == 0xfffe || r == 0xffff {
			return true
		}
	}
	return false
}

func parseSankey(src string, db *SankeyDb) {
	toks := tokenize(src)
	typ := func(i int) int {
		if i < 0 || i >= len(toks.types) {
			return -1
		}
		return toks.types[i]
	}
	i := 0

	fail := func(expected string) {
		name := func(t int) string {
			if t < 0 {
				return "undefined"
			}
			return TOKEN_NAMES[t]
		}
		got := name(typ(i))
		if typ(i) == T_STUCK {
			got = name(typ(i - 1))
		}
		start := len(src)
		if i < len(toks.starts) {
			start = toks.starts[i]
		}
		panic(types.SyntaxError("sankey", src, start, "Expecting "+expected+", got '"+got+"'", false))
	}

	expect := func(t int, also string) {
		if typ(i) != t {
			fail("'" + TOKEN_NAMES[t] + "'" + also)
		}
		i++
	}

	field := func() string {
		if typ(i) == T_NON_ESCAPED_TEXT {
			i++
			return toks.texts[i-1]
		}
		if typ(i) != T_DQUOTE {
			fail("'DQUOTE', 'NON_ESCAPED_TEXT'")
		}
		i++
		if typ(i) != T_ESCAPED_TEXT {
			fail("'ESCAPED_TEXT'")
		}
		text := toks.texts[i]
		i++
		expect(T_DQUOTE, "")
		return text
	}

	name := func() string {
		start := len(src)
		if i < len(toks.starts) {
			start = toks.starts[i]
		}
		name := strings.ReplaceAll(js.Trim(field()), `""`, `"`)
		// Entity codes can encode characters excluded by the lexer.
		if invalidXML(text.DecodeEntities(name)) {
			panic(types.SyntaxError("sankey", src, start, "Invalid XML character in node name", false))
		}
		return name
	}

	expect(T_SANKEY, "")
	expect(T_NEWLINE, "")
	for {
		from := name()
		expect(T_COMMA, ", 'NEWLINE', 'EOF'")
		to := name()
		expect(T_COMMA, ", 'NEWLINE', 'EOF'")
		amount := field()
		source := db.findOrCreateNode(from)
		target := db.findOrCreateNode(to)
		db.addLink(source, target, js.ParseFloat(js.Trim(amount)))
		if typ(i) != T_NEWLINE {
			break
		}
		i++
	}
	if typ(i) == T_EOF {
		i++
	}
	if typ(i) != T_END {
		fail("'NEWLINE', 'EOF'")
	}
}
