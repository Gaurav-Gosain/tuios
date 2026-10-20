// Ported from pele src/svg/root.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package svg

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// A rectangle's corner radius comes from a variable. Browsers only lately take a variable in the
// `rx` attribute, and all of them take one in a style, so that is where it is written. It goes
// first there, for a radius the diagram's author set to come after it and win.
const rootRounded = ` rx="` + RADIUS + `"`

// splitTag reports whether a part ends inside a tag, which the next part may
// finish.
func splitTag(parts []string) bool {
	for _, part := range parts {
		if strings.LastIndexByte(part, '<') > strings.LastIndexByte(part, '>') {
			return true
		}
	}
	return false
}

// writeRounded writes content to b with the corner radius of each <rect> with
// rx="var(--pele-radius,4px)" moved into its style. pele does it with
// /<rect\b[^>]*>/g; a scan does the same without a regexp pass.
//
// It returns how many rects it changed, and whether taking an attribute out
// may have joined text into a new var(--_...).
func writeRounded(b *strings.Builder, content string) (moved int, joined bool) {
	last := 0
	for i := 0; ; {
		at := strings.Index(content[i:], "<rect")
		if at < 0 {
			break
		}
		at += i
		next := at + len("<rect")
		// \b: the tag name ends there.
		if next < len(content) && isWordByte(content[next]) {
			i = next
			continue
		}
		end := strings.IndexByte(content[next:], '>')
		if end < 0 {
			break
		}
		end += next + 1
		tag := content[at:end]
		b.WriteString(content[last:at])
		if m, j := roundTag(b, tag); m {
			moved++
			joined = joined || j
		}
		last = end
		i = end
	}
	b.WriteString(content[last:])
	return moved, joined
}

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func roundTag(b *strings.Builder, tag string) (moved, joined bool) {
	at := strings.Index(tag, rootRounded)
	if at < 0 {
		b.WriteString(tag)
		return false, false
	}
	// A var(--_...) across the cut would take the byte before it.
	joined = at > 0 && varByte(tag[at-1])
	rest := tag[:at] + tag[at+len(rootRounded):]
	if style := strings.Index(rest, ` style="`); style >= 0 {
		WriteStrings(b, rest[:style+8], "rx:var(--_r);", rest[style+8:])
		return true, joined
	}
	end := len(rest) - 1
	if strings.HasSuffix(rest, "/>") {
		end = len(rest) - 2
	}
	WriteStrings(b, rest[:end], ` style="rx:var(--_r)"`, rest[end:])
	return true, joined
}

// The weights of bold text, each set only in a diagram that has such text.
var weights = [...][2]string{
	{"--_tw", "title"},
	{"--_hw", "heading"},
	{"--_w", "bold"},
}

type Accessible struct {
	AccTitle string
	AccDescr string
}

// rootVars are the variables of the root style a drawing uses.
type rootVars struct {
	radius bool
	bold   [len(weights)]bool
}

// usedVars finds the variables the parts use, in one pass over each var(--
// in them, and reports whether they hold rx="var(--pele-radius,4px)", which
// writeRounded may turn into var(--_r).
func usedVars(parts ...string) (v rootVars, movable bool) {
	for _, content := range parts {
		for i := 0; ; {
			at := strings.Index(content[i:], "var(--")
			if at < 0 {
				break
			}
			start := i + at
			i = start + len("var(")
			rest := content[i:]
			if strings.HasPrefix(rest, "--_r)") {
				v.radius = true
				continue
			}
			if start >= len(` rx="`) && strings.HasPrefix(content[start-len(` rx="`):], rootRounded) {
				movable = true
				continue
			}
			for k, pair := range weights {
				if strings.HasPrefix(rest, pair[0]) && strings.HasPrefix(rest[len(pair[0]):], ")") {
					v.bold[k] = true
				}
			}
		}
	}
	return v, movable
}

// varByte reports whether c can be part of a var(--_...) the root style
// looks for.
func varByte(c byte) bool {
	return strings.IndexByte("var(-_thw)", c) >= 0
}

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
	// open writes the root's start tag and the head.
	// open writes the root's start tag and the head.
	open := func(b *strings.Builder, v rootVars) {
		WriteStrings(b, `<svg xmlns="http://www.w3.org/2000/svg" class="pele pele-`, typ, `" viewBox="0 0 `)
		writeJSNum(b, width)
		b.WriteByte(' ')
		writeJSNum(b, height)
		b.WriteString(`" width="`)
		writeJSNum(b, width)
		b.WriteString(`" height="`)
		writeJSNum(b, height)
		WriteStrings(b, `" style="`, fit, Esc(options.Theme.Style()), ROOT_STYLE)
		if v.radius {
			b.WriteString(";--_r:" + RADIUS)
		}
		for k, pair := range weights {
			if v.bold[k] {
				WriteStrings(b, ";", pair[0], ":var(--pele-", pair[1], "-weight,bold)")
			}
		}
		// The fonts the labels were measured in are the ones they are drawn
		// in, unless the page names others. The rest of what text inherits
		// is pinned, as none of it was measured. Esc works byte by byte, so
		// the font lists are escaped on their own.
		WriteStrings(b, ";--_fm:var(--pele-font-mono,", Esc(families(options.FontFamilyMono, "monospace")), ");font:")
		writeJSNum(b, fontSize)
		WriteStrings(b, "px sans-serif;letter-spacing:normal;word-spacing:normal;text-transform:none;direction:ltr;font-family:var(--pele-font,",
			Esc(families(options.FontFamily, "sans-serif")),
			`)" fill="var(--_fg)" role="graphics-document document" aria-roledescription="`, typ, `"`, aria, `>`, head)
	}
	// The parts of the drawing, in order. Joined, they are what pele calls
	// content. The root's style depends on the variables the content uses
	// once the radius has moved; the parts give a guess, which the written
	// content confirms.
	if len(parts) > 1 && splitTag(parts) {
		parts = []string{strings.Join(parts, "")}
	}
	found, movable := usedVars(parts...)
	guess := found
	guess.radius = found.radius || movable
	total := 1024 + len(head) + len(aria)
	for _, part := range parts {
		total += len(part)
	}
	var b strings.Builder
	b.Grow(total)
	open(&b, guess)
	root := b.Len()
	moved, joined := 0, false
	for i, part := range parts {
		m, j := writeRounded(&b, part)
		moved += m
		// A var(--_...) across two parts would take the last byte of the
		// first, and none ends in a tag's ">".
		joined = joined || j || (i < len(parts)-1 && part != "" && part[len(part)-1] != '>')
	}
	b.WriteString("</svg>")
	out := b.String()
	content := out[root : len(out)-len("</svg>")]
	// Moving a radius takes out text with no var(--_ in it and adds
	// var(--_r), so what the parts use is what the content uses, unless a
	// cut or a join between parts made a new one; then the content is read.
	used := found
	used.radius = found.radius || moved > 0
	if joined {
		used, _ = usedVars(content)
	}
	if used != guess {
		var again strings.Builder
		again.Grow(len(out) + 256)
		open(&again, used)
		again.WriteString(content)
		again.WriteString("</svg>")
		return again.String()
	}
	return out
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
