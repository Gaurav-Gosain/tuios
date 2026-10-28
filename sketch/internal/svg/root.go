// Ported from pele src/svg/root.ts at 8aff183be57021b533f319a29341a529be934b08.

package svg

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// The weights of bold text, each set only in a diagram that has such text.
var WEIGHTS = [...][2]string{
	{"tw", "title"},
	{"hw", "heading"},
	{"w", "bold"},
}

// usedNames are the root variables pele's RE_USED, /var\(--_(r|tw|hw|w)\)/g,
// finds; the radius first, then WEIGHTS in order.
var usedNames = [...]string{"r", "tw", "hw", "w"}

type Accessible struct {
	AccTitle string
	AccDescr string
}

// usedVars marks in used each name of var(--_name) that content holds, as
// content.matchAll(RE_USED) does in one pass over it.
func usedVars(content string, used *[len(usedNames)]bool) {
	for i := 0; ; {
		at := strings.Index(content[i:], "var(--_")
		if at < 0 {
			return
		}
		i += at + len("var(--_")
		rest := content[i:]
		for k, name := range usedNames {
			if strings.HasPrefix(rest, name) && strings.HasPrefix(rest[len(name):], ")") {
				used[k] = true
				break
			}
		}
	}
}

// varByte reports whether c can be part of a var(--_...) that RE_USED
// matches, so a match may run across the end of a part that ends in c.
func varByte(c byte) bool {
	return strings.IndexByte("var(-_thw", c) >= 0
}

// SvgDocument writes the root element around content, which the parts
// are, joined.
func SvgDocument(typ string, width, height, fontSize float64, options types.RenderOptions, acc Accessible, parts ...string) string {
	prefix := Esc(types.Or(options.IDPrefix, "pele"))
	head := ""
	aria := ""
	if acc.AccTitle != "" {
		head += `<title id="` + prefix + `-title">` + EscText(acc.AccTitle) + `</title>`
		aria += ` aria-labelledby="` + prefix + `-title"`
	}
	if acc.AccDescr != "" {
		head += `<desc id="` + prefix + `-desc">` + EscText(acc.AccDescr) + `</desc>`
		aria += ` aria-describedby="` + prefix + `-desc"`
	}
	fit := "max-width:100%;height:auto;"
	if options.Responsive != nil && !*options.Responsive {
		fit = ""
	}
	var used [len(usedNames)]bool
	total := 1024 + len(head) + len(aria)
	across := false
	for i, part := range parts {
		total += len(part)
		usedVars(part, &used)
		// A match may run across the end of a part that ends inside one.
		across = across || i < len(parts)-1 && part != "" && varByte(part[len(part)-1])
	}
	if across {
		usedVars(strings.Join(parts, ""), &used)
	}
	var b strings.Builder
	b.Grow(total)
	WriteStrings(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="pele pele-`, typ, `" viewBox="0 0 `)
	writeJSNum(&b, width)
	b.WriteByte(' ')
	writeJSNum(&b, height)
	b.WriteString(`" width="`)
	writeJSNum(&b, width)
	b.WriteString(`" height="`)
	writeJSNum(&b, height)
	WriteStrings(&b, `" style="`, fit, Esc(options.Theme.Style()), ROOT_STYLE)
	if used[0] {
		b.WriteString(";--_r:" + RADIUS)
	}
	for k, pair := range WEIGHTS {
		if used[k+1] {
			WriteStrings(&b, ";--_", pair[0], ":var(--pele-", pair[1], "-weight,bold)")
		}
	}
	// The fonts the labels were measured in are the ones they are drawn
	// in, unless the page names others. The rest of what text inherits
	// is pinned, as none of it was measured. Esc works byte by byte, so
	// the font lists are escaped on their own.
	WriteStrings(&b, ";--_fm:var(--pele-font-mono,", Esc(families(options.FontFamilyMono, "monospace")), ");font:")
	writeJSNum(&b, fontSize)
	WriteStrings(&b, "px sans-serif;letter-spacing:normal;word-spacing:normal;text-transform:none;direction:ltr;font-family:var(--pele-font,",
		Esc(families(options.FontFamily, "sans-serif")),
		`)" fill="var(--_fg)" role="graphics-document document" aria-roledescription="`, typ, `"`, aria, `>`, head)
	for _, part := range parts {
		b.WriteString(part)
	}
	b.WriteString("</svg>")
	return b.String()
}

// writeJSNum writes js.Num(x) to b.
//
//go:noinline
func writeJSNum(b *strings.Builder, x float64) {
	if x != 0 && x == math.Trunc(x) && math.Abs(x) < 1e21 {
		// js.Num formats a whole number with FormatFloat(x, 'f', -1).
		var buf [32]byte
		b.Write(strconv.AppendFloat(buf[:0], x, 'f', -1, 64))
		return
	}
	b.WriteString(js.Num(x))
}
