// Ported from pele src/text/label.ts at 8aff183be57021b533f319a29341a529be934b08.
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

// Lines of one wrapped label sit closer, so the label reads as one block.
const WRAPPED_LINE_HEIGHT = 1.3

// snug is the line height of a label with the given number of lines.
func snug(lines int, size, lineHeight float64) float64 {
	if lines > 1 {
		return js.Round(size * WRAPPED_LINE_HEIGHT)
	}
	return lineHeight
}

var tagStyles = map[string]int{
	"b":      Bold,
	"strong": Bold,
	"i":      Italic,
	"em":     Italic,
	"cite":   Italic,
	"dfn":    Italic,
	"var":    Italic,
	"code":   Mono,
	"kbd":    Mono,
	"samp":   Mono,
	"tt":     Mono,
	"sub":    Sub,
	"sup":    Sup,
	"u":      Underline,
	"ins":    Underline,
}

const shift = Sub | Sup

// htmlTags are the tags a label drops while it keeps their text. Anything else
// shaped like a tag, such as the type in List<T>, is text.
var htmlTags = func() map[string]bool {
	out := map[string]bool{}
	for _, tag := range strings.Fields("a abbr address article aside audio b bdi bdo big blockquote body br button caption center cite code col " +
		"colgroup data dd del details dfn dialog div dl dt em embed fieldset figcaption figure font footer form h1 h2 " +
		"h3 h4 h5 h6 head header hr html i iframe img input ins kbd label legend li link main map mark meta meter nav " +
		"noscript object ol optgroup option output p picture pre progress q rp rt ruby s samp script section select " +
		"small source span strike strong style sub summary sup table tbody td template textarea tfoot th thead time " +
		"title tr track tt u ul var video wbr") {
		out[tag] = true
	}
	return out
}()

var (
	reBreak = lazyre.New(`(?i)<br[` + js.S + `]*/?>|\\n|\n`)
	reTag   = lazyre.New(`^</?([a-zA-Z][\w-]*)(?:[` + js.S + `][^<>]*)?/?>`)
	reIcon  = lazyre.New(`(fa[bklrs]?):fa-([\w-]+)`)
	rePiece = lazyre.New(`[` + js.S + `]*(?:[` + cjkOpen + `]*[` + cjk + `][` + cjkClose + `]*|[^` + js.S + cjk + hint + `]+[` + hint + `]*[` + cjkClose + `]*|[` + hint + `]+)[` + js.S + `]*|[` + js.S + `]+`)
	reCRLF  = lazyre.New(`\r\n?`)
)

// Break between any two Chinese or Japanese characters. Hangul is left out: Korean puts spaces between words.
const (
	cjk      = `\x{2e80}-\x{9fff}\x{f900}-\x{faff}\x{ff00}-\x{ff60}`
	cjkClose = `、。，．：；！？）］｝」』】〕〉》〙〗ー…‥・ゝゞぁぃぅぇぉっゃゅょゎァィゥェォッャュョヮ,.:;!?)\]}`
	cjkOpen  = `（［｛「『【〔〈《〘〖(\[{`
	// Break hints: a zero-width space, and a soft hyphen, which shows as '-' where a line breaks.
	hint = `\x{200b}\x{00ad}`
)

// hasHint is RE_HINT.test(text).
func hasHint(text string) bool {
	return strings.ContainsRune(text, 0x200b) || strings.ContainsRune(text, 0xad)
}

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

// openTags is the HTML emphasis open across the lines of a label.
type openTags struct {
	style int
	// Sub/sup stack; only the innermost tag shifts text.
	shifts []int
}

// Splits inline HTML and, for markdown labels, emphasis markers into styled spans.
func inline(line *[]Span, source string, markdown bool, base int, open *openTags, m TextMeasurer, found *marks) {
	style := open.style
	marked := 0
	var text strings.Builder
	i := 0
	n := len(source)
	flush := func() {
		st := style
		if k := len(open.shifts); k > 0 {
			st |= open.shifts[k-1]
		}
		pushIcons(line, text.String(), st, m, found)
		text.Reset()
	}
	for i < n {
		ch := source[i]
		if ch == '<' {
			if loc := reTag.FindStringSubmatchIndex(source[i:]); loc != nil {
				tag := strings.ToLower(source[i+loc[2] : i+loc[3]])
				closing := i+1 < n && source[i+1] == '/'
				bit := tagStyles[tag]
				if bit&shift != 0 {
					flush()
					at := -1
					for k := len(open.shifts) - 1; k >= 0; k-- {
						if open.shifts[k] == bit {
							at = k
							break
						}
					}
					if !closing {
						open.shifts = append(open.shifts, bit)
					} else if at != -1 {
						open.shifts = append(open.shifts[:at], open.shifts[at+1:]...)
					}
				} else if bit != 0 {
					flush()
					if closing {
						open.style = (open.style &^ bit) | (base & bit)
						style = (style &^ bit) | (base & bit)
					} else {
						open.style = open.style | bit
						style = style | bit
					}
				}
				// Other HTML tags are dropped and their text kept. Anything else shaped like a tag, such
				// as the type in List<T>, is text.
				if bit != 0 || htmlTags[tag] {
					i += loc[1]
					continue
				}
			}
		} else if markdown && (ch == '*' || ch == '_') {
			double := i+1 < n && source[i+1] == ch
			bit := Italic
			ln := 1
			if double {
				bit = Bold
				ln = 2
			}
			opening := marked&bit == 0
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
			canOpen := opening && !js.IsSpace(next) && strings.Contains(source[i+ln:], marker)
			canClose := !opening && !js.IsSpace(prev)
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
	// tail holds the text of the last span of current while pieces are added to it.
	// pele adds each piece with +=, which JavaScript ropes make linear. In Go each +=
	// copies the string, so a run of pieces without width, which never breaks the
	// line, would take quadratic time. The span's Text is written from tail when it
	// is read: by a measure, a commit, or a span after it.
	var tail []byte
	stale := false
	sync := func() {
		if stale {
			current[len(current)-1].Text = string(tail)
			stale = false
		}
	}
	commit := func(hyphen bool) {
		sync()
		if plain(current) {
			last := &current[len(current)-1]
			trimmed := js.TrimEnd(last.Text)
			if hyphen && strings.HasSuffix(last.Text, "\u00ad") {
				trimmed = strings.TrimSuffix(trimmed, "\u00ad") + "-"
			}
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
	for si, span := range line {
		if span.Icon != "" || span.Math != nil {
			w := float64(size * 1.25)
			if span.Math != nil {
				w = span.Math.Width
			}
			if width > 0 && width+w > max {
				commit(false)
			}
			sync()
			s := span
			s.Width = w
			current = append(current, s)
			width += w
			continue
		}
		pieces := rePiece.FindAllString(span.Text, -1)
		// A piece ending in a soft hyphen fits if its '-' does, or if the rest of the word does, which
		// hides the hyphen. The line only grows along a word, so no later hyphen in it fits either,
		// and the remaining width of each word makes this one check.
		soft := strings.ContainsRune(span.Text, 0xad)
		var own, rest []float64
		if soft {
			n := len(pieces)
			own = make([]float64, n)
			rest = make([]float64, n+1)
			for q := n - 1; q >= 0; q-- {
				// A soft hyphen before a space is no break of its own: the space is.
				shy := strings.HasSuffix(pieces[q], "\u00ad")
				end := js.TrimEnd(pieces[q])
				if shy {
					end = strings.TrimSuffix(end, "\u00ad")
				}
				own[q] = measurer.Width(end, size, span.Style)
				// Word width beyond this span is unknown.
				switch {
				case !shy:
					rest[q] = own[q]
				case q == n-1:
					if si == len(line)-1 {
						rest[q] = own[q]
					} else {
						rest[q] = math.Inf(1)
					}
				default:
					rest[q] = own[q] + rest[q+1]
				}
			}
		}
		for p, part := range pieces {
			var partWidth float64
			if soft {
				partWidth = own[p]
			} else {
				partWidth = measurer.Width(js.TrimEnd(part), size, span.Style)
			}
			var fits bool
			if !soft || !strings.HasSuffix(part, "\u00ad") {
				fits = width+partWidth <= max
			} else {
				fits = width+measurer.Width(strings.TrimSuffix(part, "\u00ad")+"-", size, span.Style) <= max || width+rest[p] <= max
			}
			if width > 0 && !fits {
				// Breaking at whitespace leaves a preceding soft hyphen hidden.
				trimmed := js.TrimStart(part)
				commit(trimmed == part)
				part = trimmed
			}
			if plain(current) && current[len(current)-1].Style == span.Style {
				last := &current[len(current)-1]
				if !stale {
					tail = append(tail[:0], last.Text...)
				}
				tail = append(tail, part...)
				stale = true
				// Remeasuring for each zero-width piece would take quadratic time.
				if partWidth > 0 || js.TrimEnd(part) != part {
					sync()
					before := last.Width
					last.Width = measurer.Width(last.Text, size, last.Style)
					width += last.Width - before
				}
			} else {
				sync()
				w := measurer.Width(part, size, span.Style)
				current = append(current, Span{Text: part, Style: span.Style, Width: w})
				width += w
			}
		}
	}
	if len(current) > 0 || len(*out) == 0 {
		commit(false)
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
		if width <= maxWidth || !(strings.Contains(text, " ") || hasCJK(text) || hasHint(text)) {
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
		lh := snug(len(lines), size, lineHeight)
		return Label{Lines: lines, Widths: widths, Width: widest, Height: float64(len(lines)) * lh, Size: size, LineHeight: lh}
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
	open := &openTags{style: base}
	for _, part := range reBreak.Split(source, -1) {
		var line []Span
		inline(&line, js.Trim(part), markdown, base, open, measurer, found)

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
	lh := snug(len(lines), size, lineHeight)
	return Label{Lines: lines, Widths: widths, Width: width, Height: float64(len(lines)) * lh, Size: size, LineHeight: lh}
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

// Plain gives the label as lines of plain text. Not part of pele. Text in
// <sub> or <sup> is written in Unicode subscript or superscript characters,
// such as H₂O and mc², when each of its characters has one, and after "_" or
// "^" when not. Other styles, such as <code> or <u>, leave the text as it is.
func (l Label) Plain() []string {
	out := make([]string, len(l.Lines))
	for i, line := range l.Lines {
		for _, span := range line {
			switch {
			case span.Style&Sub != 0:
				out[i] += shifted(span.Text, subscripts, "_")
			case span.Style&Sup != 0:
				out[i] += shifted(span.Text, superscripts, "^")
			default:
				out[i] += span.Text
			}
		}
	}
	return out
}

var (
	subscripts   = scriptTable("0123456789+-=()aehijklmnoprstuvx", "₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎ₐₑₕᵢⱼₖₗₘₙₒₚᵣₛₜᵤᵥₓ")
	superscripts = scriptTable("0123456789+-=()abcdefghijklmnoprstuvwxyzABDEGHIJKLMNOPRTUVW",
		"⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐⁿᵒᵖʳˢᵗᵘᵛʷˣʸᶻᴬᴮᴰᴱᴳᴴᴵᴶᴷᴸᴹᴺᴼᴾᴿᵀᵁⱽᵂ")
)

func scriptTable(from, to string) map[rune]rune {
	table := map[rune]rune{}
	shifted := []rune(to)
	for i, r := range []rune(from) {
		table[r] = shifted[i]
	}
	return table
}

// shifted writes text in the characters of table, or after mark when one of
// its characters has none: in parentheses when it is longer than one character.
func shifted(text string, table map[rune]rune, mark string) string {
	var b strings.Builder
	for _, r := range text {
		s, ok := table[r]
		if !ok {
			if utf8.RuneCountInString(text) == 1 {
				return mark + text
			}
			return mark + "(" + text + ")"
		}
		b.WriteRune(s)
	}
	return b.String()
}
