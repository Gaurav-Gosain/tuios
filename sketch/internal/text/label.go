// Ported from pele src/text/label.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.
//
// pele keeps the expressions of the label being laid out in module state
// (`marks`). The port passes them down as an argument.

package text

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

type Span struct {
	Text  string
	Style int
	// Icon is the icon's name, or "" for a span of text.
	Icon string
	// Math is the typeset expression the span draws, or nil.
	Math  *MathMarkup
	Width float64
}

type Label struct {
	Lines      [][]Span
	Widths     []float64
	Width      float64
	Height     float64
	Size       float64
	LineHeight float64
	// Present only when math needs extra line height.
	Heights []float64
}

const LINE_HEIGHT = 1.5

var (
	reBreak = lazyre.New(`(?i)<br[` + js.S + `]*/?>|\\n|\n`)
	reTag   = lazyre.New(`^</?([a-zA-Z][\w-]*)(?:[` + js.S + `][^<>]*)?/?>`)
	reIcon  = lazyre.New(`(fa[bklrs]?):fa-([\w-]+)`)
	rePiece = lazyre.New(`[` + js.S + `]*(?:[` + cjkOpen + `]*[` + cjk + `][` + cjkClose + `]*|[^` + js.S + cjk + `]+[` + cjkClose + `]*)[` + js.S + `]*|[` + js.S + `]+`)
	reCRLF  = lazyre.New(`\r\n?`)
)

// Break between any two Chinese or Japanese characters. Hangul is left out: Korean puts spaces between words.
const (
	cjk      = `\x{2e80}-\x{9fff}\x{f900}-\x{faff}\x{ff00}-\x{ff60}`
	cjkClose = `、。，．：；！？）］｝」』】〕〉》〙〗ー…‥・ゝゞぁぃぅぇぉっゃゅょゎァィゥェォッャュョヮ,.:;!?)\]}`
	cjkOpen  = `（［｛「『【〔〈《〘〖(\[{`
)

// hasCJK is /[CJK]/.test(text). The ranges are all in the BMP, so a rune
// compares as the UTF-16 unit it is in JavaScript.
func hasCJK(text string) bool {
	for _, r := range text {
		if r >= 0x2e80 && r <= 0x9fff || r >= 0xf900 && r <= 0xfaff || r >= 0xff00 && r <= 0xff60 {
			return true
		}
	}
	return false
}

// Unique placeholders protect TeX from markup parsing and keep expressions aligned when tags are removed.
const (
	MARK      = 0xe000
	MAX_MARKS = 0x1900
)

type marks struct {
	tex  []string
	math []*MathMarkup
}

// isMark is /[\ue000-\uf8ff]/ for one character. The range is in the BMP, so a
// rune compares as the UTF-16 unit it is in JavaScript.
func isMark(r rune) bool { return r >= 0xe000 && r <= 0xf8ff }

// hasMark is RE_MARK.test(text).
func hasMark(text string) bool {
	for _, r := range text {
		if isMark(r) {
			return true
		}
	}
	return false
}

// isLineEnd is a character `.` does not match in a JavaScript regular expression.
func isLineEnd(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// mathEnd is where the lazy `(.+?)\$\$` of RE_MATH (/\$\$(.+?)\$\$/g) stops for
// TeX starting at from: the index of the closing $$, or -1.
func mathEnd(raw string, from int) int {
	p := from
	for first := true; ; first = false {
		if !first && strings.HasPrefix(raw[p:], "$$") {
			return p
		}
		if p >= len(raw) {
			return -1
		}
		r, size := utf8.DecodeRuneInString(raw[p:])
		if isLineEnd(r) {
			return -1
		}
		p += size
	}
}

// markMath is raw.replace(RE_MATH, ...): each $$tex$$ becomes a placeholder
// character, up to MAX_MARKS of them. It gives raw itself when there is none.
func markMath(raw string, found *marks) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(raw); {
		k := strings.Index(raw[i:], "$$")
		if k < 0 {
			break
		}
		start := i + k
		end := mathEnd(raw, start+2)
		if end < 0 {
			i = start + 1
			continue
		}
		i = end + 2
		if len(found.tex) == MAX_MARKS {
			continue
		}
		found.tex = append(found.tex, raw[start+2:end])
		b.WriteString(raw[last:start])
		b.WriteRune(rune(MARK + len(found.tex) - 1))
		last = i
	}
	if last == 0 {
		return raw
	}
	b.WriteString(raw[last:])
	return b.String()
}

var EMPTY = Label{}

// leading is the number of characters of whitespace a line starts with, and their length in bytes.
func leading(line string) (int, int) {
	n := 0
	for i, r := range line {
		if !js.IsSpace(r) {
			return n, i
		}
		n++
	}
	return n, len(line)
}

func dedent(text string) string {
	lines := strings.Split(text, "\n")
	min := math.MaxInt
	for _, line := range lines {
		if js.Trim(line) == "" {
			continue
		}
		indent, _ := leading(line)
		if indent < min {
			min = indent
		}
	}
	if min == math.MaxInt || min == 0 {
		return text
	}
	for i, line := range lines {
		indent, _ := leading(line)
		cut := min
		if indent < cut {
			cut = indent
		}
		// Whitespace is all in the BMP, so characters are UTF-16 units here.
		at := 0
		for k := 0; k < cut; k++ {
			_, size := utf8.DecodeRuneInString(line[at:])
			at += size
		}
		lines[i] = line[at:]
	}
	return strings.Join(lines, "\n")
}

func plain(line []Span) bool {
	if len(line) == 0 {
		return false
	}
	last := line[len(line)-1]
	return last.Icon == "" && last.Math == nil
}

func pushText(line *[]Span, text string, style int, found *marks) {
	if text == "" {
		return
	}
	if found != nil && hasMark(text) {
		// pele decodes each piece of text between expressions, and the TeX of each
		// expression it could not typeset, on its own, and each joins the span
		// before it. Go would copy the span's text for each piece, so the decoded
		// pieces are joined here first. A Mermaid placeholder (ﬂ°…¶ß) can span a
		// $$ boundary, so the pieces are decoded one by one, not joined and then
		// decoded.
		var pending strings.Builder
		from := 0
		for i, r := range text {
			if !isMark(r) {
				continue
			}
			pending.WriteString(DecodeEntities(text[from:i]))
			from = i + utf8.RuneLen(r)
			at := int(r) - MARK
			if markup := found.math[at]; markup != nil {
				if pending.Len() > 0 {
					pushDecoded(line, pending.String(), style)
					pending.Reset()
				}
				*line = append(*line, Span{Style: style, Math: markup, Width: markup.Width})
			} else {
				pending.WriteString(DecodeEntities("$$" + found.tex[at] + "$$"))
			}
		}
		pending.WriteString(DecodeEntities(text[from:]))
		if pending.Len() > 0 {
			pushDecoded(line, pending.String(), style)
		}
		return
	}
	pushPlain(line, text, style)
}

func pushPlain(line *[]Span, text string, style int) {
	pushDecoded(line, DecodeEntities(text), style)
}

func pushDecoded(line *[]Span, decoded string, style int) {
	l := *line
	if plain(l) && l[len(l)-1].Style == style {
		l[len(l)-1].Text += decoded
	} else {
		*line = append(l, Span{Text: decoded, Style: style})
	}
}

func pushIcons(line *[]Span, text string, style int, m TextMeasurer, found *marks) {
	if !strings.Contains(text, ":fa-") {
		pushText(line, text, style, found)
		return
	}
	last := 0
	for _, loc := range reIcon.FindAllStringSubmatchIndex(text, -1) {
		icon := text[loc[2]:loc[3]] + ":fa-" + text[loc[4]:loc[5]]
		pushText(line, text[last:loc[0]], style, found)
		last = loc[1]
		if IconMarkup(m, icon) != "" {
			*line = append(*line, Span{Style: style, Icon: icon})
		} else if last < len(text) && text[last] == ' ' {
			// An icon the host does not have leaves no gap, nor the space that set it off from the text.
			last++
		} else if last == len(text) {
			l := *line
			if plain(l) {
				l[len(l)-1].Text = js.TrimEnd(l[len(l)-1].Text)
			}
		}
	}
	pushText(line, text[last:], style, found)
}

func isWordRune(r rune) bool {
	return r < 128 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_')
}

// Splits inline HTML and, for markdown labels, emphasis markers into styled spans.
func inline(line *[]Span, source string, markdown bool, base int, htmlStyle int, m TextMeasurer, found *marks) int {
	style := htmlStyle
	marked := 0
	var text strings.Builder
	i := 0
	n := len(source)
	flush := func() {
		pushIcons(line, text.String(), style, m, found)
		text.Reset()
	}
	for i < n {
		ch := source[i]
		if ch == '<' {
			if loc := reTag.FindStringSubmatchIndex(source[i:]); loc != nil {
				tag := strings.ToLower(source[i+loc[2] : i+loc[3]])
				closing := i+1 < n && source[i+1] == '/'
				bit := 0
				if tag == "b" || tag == "strong" {
					bit = Bold
				} else if tag == "i" || tag == "em" {
					bit = Italic
				}
				if bit != 0 {
					flush()
					if closing {
						htmlStyle = (htmlStyle &^ bit) | (base & bit)
						style = (style &^ bit) | (base & bit)
					} else {
						htmlStyle = htmlStyle | bit
						style = style | bit
					}
				}
				i += loc[1]
				continue
			}
		} else if markdown && (ch == '*' || ch == '_') {
			double := i+1 < n && source[i+1] == ch
			bit := Italic
			ln := 1
			if double {
				bit = Bold
				ln = 2
			}
			open := marked&bit == 0
			prev := ' '
			if i > 0 {
				prev, _ = utf8.DecodeLastRuneInString(source[:i])
			}
			next := ' '
			if i+ln < n {
				next, _ = utf8.DecodeRuneInString(source[i+ln:])
			}
			marker := string(ch)
			if double {
				marker += marker
			}
			canOpen := open && !js.IsSpace(next) && strings.Contains(source[i+ln:], marker)
			canClose := !open && !js.IsSpace(prev)
			wordy := ch == '_' && isWordRune(prev) && isWordRune(next)
			if (canOpen || canClose) && !wordy {
				flush()
				if canOpen {
					marked |= bit
					style |= bit
				} else {
					marked &^= bit
					style = (style &^ bit) | (base & bit)
				}
				i += ln
				continue
			}
		}
		text.WriteByte(ch)
		i++
	}
	flush()
	return htmlStyle
}

func measureSpans(line []Span, measurer TextMeasurer, size float64) float64 {
	total := 0.0
	for k := range line {
		span := &line[k]
		if span.Icon != "" {
			span.Width = size * 1.25
		} else if span.Math != nil {
			span.Width = span.Math.Width
		} else {
			span.Width = measurer.Width(span.Text, size, span.Style)
		}
		total += span.Width
	}
	return total
}

func wrap(line []Span, measurer TextMeasurer, size, max float64, out *[][]Span, widths *[]float64) {
	var current []Span
	width := 0.0
	commit := func() {
		if plain(current) {
			last := &current[len(current)-1]
			trimmed := js.TrimEnd(last.Text)
			if trimmed != last.Text {
				last.Text = trimmed
				w := measurer.Width(trimmed, size, last.Style)
				width += w - last.Width
				last.Width = w
			}
		}
		*out = append(*out, current)
		*widths = append(*widths, width)
		current = nil
		width = 0
	}
	for _, span := range line {
		if span.Icon != "" || span.Math != nil {
			w := float64(size * 1.25)
			if span.Math != nil {
				w = span.Math.Width
			}
			if width > 0 && width+w > max {
				commit()
			}
			s := span
			s.Width = w
			current = append(current, s)
			width += w
			continue
		}
		for _, piece := range rePiece.FindAllString(span.Text, -1) {
			part := piece
			if width > 0 && width+measurer.Width(js.TrimEnd(part), size, span.Style) > max {
				commit()
				part = js.TrimStart(part)
			}
			if plain(current) && current[len(current)-1].Style == span.Style {
				last := &current[len(current)-1]
				last.Text += part
				last.Width = measurer.Width(last.Text, size, last.Style)
				width = 0
				for _, s := range current {
					width += s.Width
				}
			} else {
				w := measurer.Width(part, size, span.Style)
				current = append(current, Span{Text: part, Style: span.Style, Width: w})
				width += w
			}
		}
	}
	if len(current) > 0 || len(*out) == 0 {
		commit()
	}
}

// LayoutLabel measures a label and breaks it into lines no wider than maxWidth where it can.
func LayoutLabel(raw string, markdown bool, measurer TextMeasurer, size, maxWidth float64, base int) Label {
	if raw == "" {
		return EMPTY
	}
	lineHeight := js.Round(size * LINE_HEIGHT)

	if strings.Contains(raw, "$$") && mathActive(measurer) && !hasMark(raw) {
		found := &marks{}
		marked := markMath(raw, found)
		if len(found.tex) > 0 {
			found.math = make([]*MathMarkup, len(found.tex))
			for i, tex := range found.tex {
				found.math[i] = typeset(measurer, DecodeEntities(tex), size)
			}
			return WithHeights(layoutLines(marked, markdown, measurer, size, maxWidth, base, lineHeight, found))
		}
	}

	if !hasMarkup(raw) && !strings.Contains(raw, "&") && !strings.Contains(raw, "ﬂ") {
		text := js.Trim(raw)
		width := measurer.Width(text, size, base)
		if width <= maxWidth || !(strings.Contains(text, " ") || hasCJK(text)) {
			// One span on one line: its slices share one allocation.
			one := &struct {
				lines  [1][]Span
				spans  [1]Span
				widths [1]float64
			}{}
			one.spans[0] = Span{Text: text, Style: base, Width: width}
			one.lines[0] = one.spans[:]
			one.widths[0] = width
			return Label{
				Lines:      one.lines[:],
				Widths:     one.widths[:],
				Width:      width,
				Height:     lineHeight,
				Size:       size,
				LineHeight: lineHeight,
			}
		}
		var lines [][]Span
		var widths []float64
		wrap([]Span{{Text: text, Style: base, Width: width}}, measurer, size, maxWidth, &lines, &widths)
		widest := 0.0
		for _, w := range widths {
			if w > widest {
				widest = w
			}
		}
		return Label{Lines: lines, Widths: widths, Width: widest, Height: float64(len(lines)) * lineHeight, Size: size, LineHeight: lineHeight}
	}
	return layoutLines(raw, markdown, measurer, size, maxWidth, base, lineHeight, nil)
}

// layoutLines is pele's layoutMarkup and layoutLines in one: found, when not
// nil, holds the expressions raw has placeholders for.
func layoutLines(raw string, markdown bool, measurer TextMeasurer, size, maxWidth float64, base int, lineHeight float64, found *marks) Label {
	var source string
	if markdown {
		source = js.Trim(dedent(reCRLF.ReplaceAllString(raw, "\n")))
	} else {
		source = js.Trim(raw)
	}
	var lines [][]Span
	var widths []float64
	width := 0.0
	// HTML tags can span explicit breaks; Markdown emphasis stays local to each line.
	htmlStyle := base
	for _, part := range reBreak.Split(source, -1) {
		var line []Span
		htmlStyle = inline(&line, js.Trim(part), markdown, base, htmlStyle, measurer, found)

		w := measureSpans(line, measurer, size)
		if w > maxWidth {
			wrap(line, measurer, size, maxWidth, &lines, &widths)
		} else {
			lines = append(lines, line)
			widths = append(widths, w)
		}
	}
	for _, w := range widths {
		if w > width {
			width = w
		}
	}
	return Label{Lines: lines, Widths: widths, Width: width, Height: float64(len(lines)) * lineHeight, Size: size, LineHeight: lineHeight}
}

// WithHeights gives each line of a label holding math the height that math needs.
func WithHeights(label Label) Label {
	var heights []float64
	for i, line := range label.Lines {
		h := label.LineHeight
		for _, span := range line {
			if span.Math != nil {
				h = math.Max(h, math.Ceil(span.Math.Height+label.Size/4))
			}
		}
		if h != label.LineHeight && heights == nil {
			heights = make([]float64, len(label.Lines))
			for k := range heights {
				heights[k] = label.LineHeight
			}
		}
		if heights != nil {
			heights[i] = h
		}
	}
	if heights == nil {
		if label.Heights == nil {
			return label
		}
		label.Heights = nil
		label.Height = float64(len(label.Lines)) * label.LineHeight
		return label
	}
	height := 0.0
	for _, h := range heights {
		height += h
	}
	label.Heights = heights
	label.Height = height
	return label
}

// HasMath reports whether a line holds a typeset expression.
func HasMath(line []Span) bool {
	for _, span := range line {
		if span.Math != nil {
			return true
		}
	}
	return false
}

// LineMiddle is the middle of line i, from the top of the label.
func LineMiddle(label Label, i int) float64 {
	if label.Heights == nil {
		return float64((float64(i) + 0.5) * label.LineHeight)
	}
	top := 0.0
	for k := 0; k < i; k++ {
		top += label.Heights[k]
	}
	return top + label.Heights[i]/2
}

// LabelLine is line i of a label as a label of its own.
func LabelLine(label Label, i int) Label {
	width := label.Widths[i]
	height := label.LineHeight
	if label.Heights != nil {
		height = label.Heights[i]
	}
	// The line shares the label's slices: nothing writes to them.
	line := Label{Lines: label.Lines[i : i+1], Widths: label.Widths[i : i+1], Width: width, Height: height, Size: label.Size, LineHeight: label.LineHeight}
	if height != label.LineHeight {
		line.Heights = []float64{height}
	}
	return line
}

// hasMarkup is /[<\\\n*_]|fa[bklrs]?:fa-/: whether raw may hold a break, a tag, an
// escape, emphasis or an icon.
func hasMarkup(raw string) bool {
	if strings.ContainsAny(raw, "<\\\n*_") {
		return true
	}
	for i := 0; ; {
		j := strings.Index(raw[i:], "fa")
		if j < 0 {
			return false
		}
		k := i + j + 2
		if strings.HasPrefix(raw[k:], ":fa-") || k < len(raw) && strings.IndexByte("bklrs", raw[k]) >= 0 && strings.HasPrefix(raw[k+1:], ":fa-") {
			return true
		}
		i += j + 1
	}
}

// Plain gives the label as lines of plain text. Not part of pele.
func (l Label) Plain() []string {
	out := make([]string, len(l.Lines))
	for i, line := range l.Lines {
		for _, span := range line {
			out[i] += span.Text
		}
	}
	return out
}
