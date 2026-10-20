// Ported from pele src/svg/root.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package svg

import (
	"bytes"
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
func writeRounded(b *strings.Builder, content string) {
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
		roundTag(b, tag)
		last = end
		i = end
	}
	b.WriteString(content[last:])
}

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func roundTag(b *strings.Builder, tag string) {
	at := strings.Index(tag, rootRounded)
	if at < 0 {
		b.WriteString(tag)
		return
	}
	// The tag without the attribute. A tag is short, so it is put together on
	// the stack; a long one falls back to a string.
	var stack [512]byte
	var rest []byte
	if len(tag) <= len(stack) {
		rest = append(append(stack[:0], tag[:at]...), tag[at+len(rootRounded):]...)
	} else {
		rest = []byte(tag[:at] + tag[at+len(rootRounded):])
	}
	style := bytes.Index(rest, []byte(` style="`))
	if style >= 0 {
		b.Write(rest[:style+8])
		b.WriteString("rx:var(--_r);")
		b.Write(rest[style+8:])
		return
	}
	end := len(rest) - 1
	if bytes.HasSuffix(rest, []byte("/>")) {
		end = len(rest) - 2
	}
	b.Write(rest[:end])
	b.WriteString(` style="rx:var(--_r)"`)
	b.Write(rest[end:])
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
// in them. With moved, a radius that writeRounded will move counts as
// var(--_r) already; that makes a guess, which a scan of the written content
// confirms.
func usedVars(moved bool, parts ...string) rootVars {
	var v rootVars
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
			if moved && start >= len(` rx="`) && strings.HasPrefix(content[start-len(` rx="`):], rootRounded) {
				v.radius = true
				continue
			}
			for k, pair := range weights {
				if strings.HasPrefix(rest, pair[0]) && strings.HasPrefix(rest[len(pair[0]):], ")") {
					v.bold[k] = true
				}
			}
		}
	}
	return v
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
	open := func(v rootVars) string {
		w := ""
		if v.radius {
			w = ";--_r:" + RADIUS
		}
		for k, pair := range weights {
			if v.bold[k] {
				w += ";" + pair[0] + ":var(--pele-" + pair[1] + "-weight,bold)"
			}
		}
		return `<svg xmlns="http://www.w3.org/2000/svg" class="pele pele-` + typ + `" viewBox="0 0 ` + js.Num(width) + ` ` + js.Num(height) +
			`" width="` + js.Num(width) + `" height="` + js.Num(height) + `" style="` + fit + Esc(options.Theme.Style()) + ROOT_STYLE + w +
			Esc(FontStyle(fontSize, options.FontFamily, options.FontFamilyMono)) +
			`" fill="var(--_fg)" role="graphics-document document" aria-roledescription="` + typ + `"` + aria + `>` +
			head
	}
	// The parts of the drawing, in order. Joined, they are what pele calls
	// content. The root's style depends on the variables the content uses
	// once the radius has moved; the parts give a guess, which the written
	// content confirms.
	if len(parts) > 1 && splitTag(parts) {
		parts = []string{strings.Join(parts, "")}
	}
	guess := usedVars(true, parts...)
	root := open(guess)
	total := len(root) + len("</svg>")
	for _, part := range parts {
		total += len(part)
	}
	var b strings.Builder
	b.Grow(total)
	b.WriteString(root)
	for _, part := range parts {
		writeRounded(&b, part)
	}
	b.WriteString("</svg>")
	out := b.String()
	content := out[len(root) : len(out)-len("</svg>")]
	if used := usedVars(false, content); used != guess {
		return open(used) + content + "</svg>"
	}
	return out
}
