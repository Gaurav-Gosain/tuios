// Ported from pele src/svg/root.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package svg

import (
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// A rectangle's corner radius comes from a variable. Browsers only lately take a variable in the
// `rx` attribute, and all of them take one in a style, so that is where it is written. It goes
// first there, for a radius the diagram's author set to come after it and win.
const rootRounded = ` rx="` + RADIUS + `"`

// rounded moves the corner radius of each <rect> with rx="var(--pele-radius,4px)"
// into its style. pele does it with /<rect\b[^>]*>/g; a scan does the same
// without a regexp pass over the whole drawing.
func rounded(content string) string {
	if !strings.Contains(content, rootRounded) {
		return content
	}
	var b strings.Builder
	b.Grow(len(content) + 64)
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
		b.WriteString(roundTag(tag))
		last = end
		i = end
	}
	b.WriteString(content[last:])
	return b.String()
}

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func roundTag(tag string) string {
	at := strings.Index(tag, rootRounded)
	if at < 0 {
		return tag
	}
	rest := tag[:at] + tag[at+len(rootRounded):]
	style := strings.Index(rest, ` style="`)
	if style >= 0 {
		return rest[:style+8] + "rx:var(--_r);" + rest[style+8:]
	}
	end := len(rest) - 1
	if strings.HasSuffix(rest, "/>") {
		end = len(rest) - 2
	}
	return rest[:end] + ` style="rx:var(--_r)"` + rest[end:]
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

func SvgDocument(typ string, width, height, fontSize float64, options types.RenderOptions, acc Accessible, content string) string {
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
	content = rounded(content)
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
