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

// rounded moves the corner radius of each <rect> with rx="var(--pele-radius,4px)"
// into its style, in the content the parts make when joined. pele does it with
// /<rect\b[^>]*>/g; a scan does the same without a regexp pass over the whole
// drawing.
func rounded(parts []string) string {
	total := 0
	round := false
	for _, part := range parts {
		total += len(part)
		if strings.LastIndexByte(part, '<') > strings.LastIndexByte(part, '>') {
			// The part ends inside a tag, which the next one may finish. Scan
			// the joined content, so that a tag across parts is seen whole.
			return roundedIn(strings.Join(parts, ""))
		}
		round = round || strings.Contains(part, rootRounded)
	}
	if !round {
		return strings.Join(parts, "")
	}
	var b strings.Builder
	b.Grow(total)
	for _, part := range parts {
		writeRounded(&b, part)
	}
	return b.String()
}

func roundedIn(content string) string {
	if !strings.Contains(content, rootRounded) {
		return content
	}
	var b strings.Builder
	b.Grow(len(content))
	writeRounded(&b, content)
	return b.String()
}

// writeRounded writes content to b with the radius of each rect moved.
func writeRounded(b *strings.Builder, content string) {
	if !strings.Contains(content, rootRounded) {
		b.WriteString(content)
		return
	}
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
var weights = [][2]string{
	{"--_tw", "title"},
	{"--_hw", "heading"},
	{"--_w", "bold"},
}

type Accessible struct {
	AccTitle string
	AccDescr string
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
	// The parts of the drawing, in order. Joined, they are what pele calls content.
	content := rounded(parts)
	w := ""
	if strings.Contains(content, "var(--_r)") {
		w = ";--_r:" + RADIUS
	}
	for _, pair := range weights {
		if strings.Contains(content, "var("+pair[0]+")") {
			w += ";" + pair[0] + ":var(--pele-" + pair[1] + "-weight,bold)"
		}
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" class="pele pele-` + typ + `" viewBox="0 0 ` + js.Num(width) + ` ` + js.Num(height) +
		`" width="` + js.Num(width) + `" height="` + js.Num(height) + `" style="` + fit + Esc(options.Theme.Style()) + ROOT_STYLE + w +
		Esc(FontStyle(fontSize, options.FontFamily, options.FontFamilyMono)) +
		`" fill="var(--_fg)" role="graphics-document document" aria-roledescription="` + typ + `"` + aria + `>` +
		head + content + `</svg>`
}
