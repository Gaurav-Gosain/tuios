// Ported from pele src/diagrams/sankey/parser.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package sankey

import (
	"math"
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
// When st is not nil it records the edits of each step, so an offset maps back to text.
func prepareTextForParsing(text string, st *[][]types.Edit) string {
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
	sliced := text[start:end]
	var breaks *[]types.Edit
	if st != nil {
		breaks = &[]types.Edit{}
	}
	collapsed := collapseBreaks(sliced, breaks)
	trimmed := js.Trim(collapsed)
	if st != nil {
		lead := len(collapsed) - len(js.TrimStart(collapsed))
		*st = append(*st,
			[]types.Edit{{From: 0, To: start}, {From: end, To: len(text)}},
			*breaks,
			[]types.Edit{{From: 0, To: lead}, {From: lead + len(trimmed), To: len(collapsed)}})
	}
	return trimmed
}

// collapseBreaks is .replace(/[\n\r]+/g, '\n'). It lists each run it replaces in edits
// when edits is not nil.
func collapseBreaks(text string, edits *[]types.Edit) string {
	if edits == nil && !strings.ContainsAny(text, "\r") && !strings.Contains(text, "\n\n") {
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
		from := i
		for i+1 < len(text) && (text[i+1] == '\n' || text[i+1] == '\r') {
			i++
		}
		if edits != nil {
			*edits = append(*edits, types.Edit{From: from, To: i + 1, Length: 1})
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

// parseSankey reports errors in source, the text before prepareTextForParsing.
func parseSankey(src string, db *SankeyDb, source string) {
	toks := tokenize(src)
	typ := func(i int) int {
		if i < 0 || i >= len(toks.types) {
			return -1
		}
		return toks.types[i]
	}
	i := 0

	fail := func(at int, detail string) {
		var st [][]types.Edit
		if source != src {
			prepareTextForParsing(source, &st)
		}
		// Map EOF before the trimmed trailing blanks.
		offset := types.InputOffset(st, at)
		if at > 0 && at >= len(src) {
			offset = types.InputOffset(st, len(src)-1) + 1
		}
		panic(types.SyntaxError("sankey", source, offset, detail, false))
	}

	failExpecting := func(expected string) {
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
		fail(start, "Expecting "+expected+", got '"+got+"'")
	}

	expect := func(t int, also string) {
		if typ(i) != t {
			failExpecting("'" + TOKEN_NAMES[t] + "'" + also)
		}
		i++
	}

	field := func() string {
		if typ(i) == T_NON_ESCAPED_TEXT {
			i++
			return toks.texts[i-1]
		}
		if typ(i) != T_DQUOTE {
			failExpecting("'DQUOTE', 'NON_ESCAPED_TEXT'")
		}
		i++
		if typ(i) != T_ESCAPED_TEXT {
			failExpecting("'ESCAPED_TEXT'")
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
			fail(start, "Invalid XML character in node name")
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
		start := len(src)
		if i < len(toks.starts) {
			start = toks.starts[i]
		}
		// Keep Mermaid's numeric prefixes, but reject non-finite values.
		value := js.ParseFloat(js.Trim(field()))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			fail(start, "Expecting a number")
		}
		db.addLink(db.findOrCreateNode(from), db.findOrCreateNode(to), value)
		if typ(i) != T_NEWLINE {
			break
		}
		i++
	}
	if typ(i) == T_EOF {
		i++
	}
	if typ(i) != T_END {
		failExpecting("'NEWLINE', 'EOF'")
	}
}
