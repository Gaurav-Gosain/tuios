// Ported from pele src/svg/builder.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package svg writes the SVG that every diagram type shares: text, shapes,
// edges, clusters and the root element.
package svg

import (
	"math"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
	"tuios.dev/sketch/internal/text"
)

type IconResolver = text.IconResolver

func needsEsc(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '&' || c == '<' || c == '>' || c == '"':
			return true
		case c <= 0x08 || c == 0x0b || c == 0x0c || (c >= 0x0e && c <= 0x1f) || c == 0x7f:
			return true
		case nonCharAt(s, i):
			return true
		}
	}
	return false
}

// nonCharAt reports whether U+FFFE or U+FFFF starts at byte i. Valid UTF-8 holds no
// surrogate, the other characters RE_INVALID_XML names.
func nonCharAt(s string, i int) bool {
	return s[i] == 0xef && i+2 < len(s) && s[i+1] == 0xbf && (s[i+2] == 0xbe || s[i+2] == 0xbf)
}

// InvalidXML is RE_INVALID_XML.test(s): a character XML does not allow, or DEL.
// Removing these characters in Esc() can make validated values unsafe.
func InvalidXML(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x08 || c == 0x0b || c == 0x0c || (c >= 0x0e && c <= 0x1f) || c == 0x7f || nonCharAt(s, i) {
			return true
		}
	}
	return false
}

// Esc escapes text for XML and drops invalid XML characters and DEL.
func Esc(s string) string {
	if !needsEsc(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '"':
			b.WriteString("&quot;")
		case c <= 0x08 || c == 0x0b || c == 0x0c || (c >= 0x0e && c <= 0x1f) || c == 0x7f:
		case nonCharAt(s, i):
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// EscText escapes text that is only ever read as text: a tooltip, an accessible name, a `data-id`. Entity
// codes are turned into their characters first. A value that was checked before it is written,
// such as a style, a colour or a URL, must go through Esc() instead, so that what was checked is
// what is written.
func EscText(s string) string {
	return Esc(text.DecodeEntities(s))
}

// Num writes a coordinate with at most two decimals.
func Num(value float64) string {
	n := js.Round(value * 100)
	if n == 0 {
		return "0"
	}
	if n > -1e11 && n < 1e11 {
		if n > 0 && n < 10000 {
			// n is whole. A small whole number of units comes from strconv,
			// which keeps these, so it costs no allocation.
			if i := int(n); i%100 == 0 {
				return strconv.Itoa(i / 100)
			}
		}
		var buf [24]byte
		return string(appendHundredths(buf[:0], n))
	}
	return js.Num(n / 100)
}

// AppendNum appends Num(value) to b.
func AppendNum(b []byte, value float64) []byte {
	n := js.Round(value * 100)
	if n == 0 {
		return append(b, '0')
	}
	if n > -1e11 && n < 1e11 {
		return appendHundredths(b, n)
	}
	return append(b, js.Num(n/100)...)
}

// WriteStrings writes each of strs to b. One call in place of a run of
// WriteString calls keeps the code that writes markup small.
//
//go:noinline
func WriteStrings(b *strings.Builder, strs ...string) {
	for _, s := range strs {
		b.WriteString(s)
	}
}

// WriteNum writes Num(value) to b. It is called from many places, and kept
// out of line so each call stays small.
//
//go:noinline
func WriteNum(b *strings.Builder, value float64) {
	var buf [32]byte
	b.Write(AppendNum(buf[:0], value))
}

// appendHundredths writes n/100 for a whole n with 0 < |n| < 1e11: the whole
// part, then up to two decimals without trailing zeros.
func appendHundredths(b []byte, n float64) []byte {
	if n < 0 {
		b = append(b, '-')
		n = -n
	}
	q := math.Floor(n / 100)
	b = strconv.AppendInt(b, int64(q), 10)
	r := int(n - float64(q*100))
	if r == 0 {
		return b
	}
	b = append(b, '.', byte('0'+r/10))
	if r%10 != 0 {
		b = append(b, byte('0'+r%10))
	}
	return b
}

// LabelSvg writes a measured label as SVG text centered on (cx, cy).
func LabelSvg(label text.Label, cx, cy float64, attrs string, icons IconResolver) string {
	lines := label.Lines
	if len(lines) == 0 {
		return ""
	}
	lh := label.LineHeight
	heights := label.Heights
	shift := float64(label.Size * 0.35)
	top := cy - label.Height/2

	if len(lines) == 1 && len(lines[0]) == 1 && lines[0][0].Icon == "" && lines[0][0].Math == nil && lines[0][0].Style == 0 {
		text := Esc(lines[0][0].Text)
		var b strings.Builder
		b.Grow(len(attrs) + len(text) + 72)
		WriteStrings(&b, `<text`, attrs, ` x="`)
		WriteNum(&b, cx)
		b.WriteString(`" y="`)
		WriteNum(&b, top+lh/2+shift)
		WriteStrings(&b, `" text-anchor="middle">`, text, `</text>`)
		return b.String()
	}

	shared := SharedStyle(label)
	given := attrs
	var look *mathLook
	attrs = TextStyle(attrs, shared)
	var body, extra strings.Builder
	for i, line := range lines {
		h := lh
		if heights != nil {
			h = heights[i]
		}
		mid := top + h/2
		y := mid + shift
		placed := false
		for _, span := range line {
			if span.Icon != "" || span.Math != nil {
				placed = true
			}
		}
		if placed {
			x := cx - label.Widths[i]/2
			for _, span := range line {
				if span.Math != nil {
					box := `x="` + Num(x) + `" y="` + Num(mid-span.Math.Height/2) + `" width="` + Num(span.Math.Width) + `" height="` + Num(span.Math.Height) + `"`
					if look == nil {
						look = lookOfMath(given)
					}
					if !look.haloed {
						extra.WriteString(mathSvg(span.Math, box, label.Size, look.attrs))
					} else {
						extra.WriteString(`<rect ` + box + ` rx="2" fill="` + look.halo + `"/>`)
					}
				} else if span.Icon != "" {
					side := label.Size * 1.1
					inner := ""
					if icons != nil {
						inner = icons(span.Icon)
					}
					extra.WriteString(`<svg class="pele-icon" data-icon="` + EscText(span.Icon) + `" x="` + Num(x) + `" y="` + Num(mid-side/2) +
						`" width="` + Num(side) + `" height="` + Num(side) + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + inner + `</svg>`)
				} else if span.Text != "" {
					body.WriteString(`<tspan x="` + Num(x) + `" y="` + Num(y) + `" text-anchor="start"` + SpanStyle(span.Style&^shared) + `>` + Esc(span.Text) + `</tspan>`)
				}
				x += span.Width
			}
		} else {
			for j, span := range line {
				pos := ""
				if j == 0 {
					pos = ` x="` + Num(cx) + `" y="` + Num(y) + `"`
				}
				body.WriteString(`<tspan` + pos + SpanStyle(span.Style&^shared) + `>` + Esc(span.Text) + `</tspan>`)
			}
			if len(line) == 0 {
				body.WriteString(`<tspan x="` + Num(cx) + `" y="` + Num(y) + `"> </tspan>`)
			}
		}
		top += h
	}
	return `<text` + attrs + ` text-anchor="middle" xml:space="preserve">` + body.String() + `</text>` + extra.String()
}

var (
	reFill         = lazyre.New(` fill="([^"]*)"`)
	reStroke       = lazyre.New(` stroke="([^"]*)"`)
	reStyle        = lazyre.New(` style="([^"]*)"`)
	reStyleFill    = lazyre.New(`(?:^|;)[` + js.S + `]*fill:([^;]*)`)
	reStyleOpacity = lazyre.New(`(?:^|;)[` + js.S + `]*opacity:([^;]*)`)
)

// Copy only color and opacity to preserve measured math dimensions.
// Use a solid patch for the cluster title's outline to hide edges behind math.
type mathLook struct {
	attrs string
	// haloed is pele's `halo !== undefined`.
	haloed bool
	halo   string
}

// group is RE.exec(s)?.[1], and whether it matched.
func group(re *lazyre.Regexp, s string) (string, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func lookOfMath(attrs string) *mathLook {
	style, _ := group(reStyle, attrs)
	fill, ok := group(reStyleFill, style)
	if !ok {
		fill, _ = group(reFill, attrs)
	}
	fill = js.Trim(fill)
	if fill == "none" {
		halo, ok := group(reStroke, attrs)
		if !ok {
			halo = "none"
		}
		return &mathLook{haloed: true, halo: halo}
	}
	out := ""
	if fill != "" {
		out = ` color="` + fill + `"`
	}
	if opacity, ok := group(reStyleOpacity, style); ok {
		if opacity = js.Trim(opacity); opacity != "" {
			out += ` opacity="` + opacity + `"`
		}
	}
	return &mathLook{attrs: out}
}

// Resolver markup is trusted.
func mathSvg(math *text.MathMarkup, box string, size float64, attrs string) string {
	if !math.HTML {
		return `<svg class="pele-math" ` + box + ` overflow="visible"` + attrs + `>` + math.Markup + `</svg>`
	}
	return `<foreignObject class="pele-math" ` + box + ` overflow="visible"` + attrs + `>` +
		`<div xmlns="http://www.w3.org/1999/xhtml" style="display:flex;align-items:center;justify-content:center;width:100%;height:100%;font-size:` + Num(size) + `px;line-height:normal;white-space:nowrap">` + math.Markup + `</div>` +
		`</foreignObject>`
}

const (
	BOLD      = ` font-weight="var(--_w)"`
	ITALIC    = ` font-style="italic"`
	UNDERLINE = ` text-decoration="underline"`
	MONO      = ` font-family="var(--_fm)"`
	SUB       = ` baseline-shift="sub" font-size="smaller"`
	SUP       = ` baseline-shift="super" font-size="smaller"`
)

func SpanStyle(style int) string {
	s := ""
	if style&text.Bold != 0 {
		s += BOLD
	}
	if style&text.Italic != 0 {
		s += ITALIC
	}
	if style&text.Mono != 0 {
		s += MONO
	}
	if style&text.Underline != 0 {
		s += UNDERLINE
	}
	if style&text.Sub != 0 {
		s += SUB
	} else if style&text.Sup != 0 {
		s += SUP
	}
	return s
}

// SharedStyle is the emphasis every span of a label has. It is written once, on the text element, where a
// CSS rule for the label reaches it.
func SharedStyle(label text.Label) int {
	shared := text.Bold | text.Italic | text.Mono
	any := false
	for _, line := range label.Lines {
		for _, span := range line {
			if span.Icon != "" || span.Text == "" {
				continue
			}
			shared &= span.Style
			any = true
		}
	}
	if any {
		return shared
	}
	return 0
}

// TextStyle adds that emphasis to a text element's attributes, unless they give a weight of their own.
func TextStyle(attrs string, shared int) string {
	if shared&text.Bold != 0 && !strings.Contains(attrs, ` font-weight="`) {
		attrs += BOLD
	}
	if shared&text.Italic != 0 && !strings.Contains(attrs, ` font-style="`) {
		attrs += ITALIC
	}
	if shared&text.Mono != 0 && !strings.Contains(attrs, ` font-family="`) {
		attrs += MONO
	}
	return attrs
}
