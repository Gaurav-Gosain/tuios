// Ported from pele src/text/label.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package text

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type Span struct {
	Text  string
	Style int
	// Icon is the icon's name, or "" for a span of text.
	Icon  string
	Width float64
}

type Label struct {
	Lines      [][]Span
	Widths     []float64
	Width      float64
	Height     float64
	Size       float64
	LineHeight float64
}

const LINE_HEIGHT = 1.5

var (
	reBreak  = regexp.MustCompile(`(?i)<br[` + js.S + `]*/?>|\\n|\n`)
	reTag    = regexp.MustCompile(`^</?([a-zA-Z][\w-]*)(?:[` + js.S + `][^<>]*)?/?>`)
	reIcon   = regexp.MustCompile(`(fa[bklrs]?):fa-([\w-]+)`)
	reMarkup = regexp.MustCompile(`[<\\\n*_]|fa[bklrs]?:fa-`)
	rePieces = regexp.MustCompile(`[` + js.S + `]*[^` + js.S + `]+[` + js.S + `]*|[` + js.S + `]+`)
	reCRLF   = regexp.MustCompile(`\r\n?`)
)

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

func pushText(line *[]Span, text string, style int) {
	if text == "" {
		return
	}
	decoded := DecodeEntities(text)
	l := *line
	if len(l) > 0 && l[len(l)-1].Style == style && l[len(l)-1].Icon == "" {
		l[len(l)-1].Text += decoded
	} else {
		*line = append(l, Span{Text: decoded, Style: style})
	}
}

func pushIcons(line *[]Span, text string, style int, m TextMeasurer) {
	if !strings.Contains(text, ":fa-") {
		pushText(line, text, style)
		return
	}
	last := 0
	for _, loc := range reIcon.FindAllStringSubmatchIndex(text, -1) {
		icon := text[loc[2]:loc[3]] + ":fa-" + text[loc[4]:loc[5]]
		pushText(line, text[last:loc[0]], style)
		last = loc[1]
		if IconMarkup(m, icon) != "" {
			*line = append(*line, Span{Style: style, Icon: icon})
		} else if last < len(text) && text[last] == ' ' {
			// An icon the host does not have leaves no gap, nor the space that set it off from the text.
			last++
		} else if last == len(text) {
			l := *line
			if len(l) > 0 && l[len(l)-1].Icon == "" {
				l[len(l)-1].Text = js.TrimEnd(l[len(l)-1].Text)
			}
		}
	}
	pushText(line, text[last:], style)
}

func isWordRune(r rune) bool {
	return r < 128 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_')
}

// Splits inline HTML and, for markdown labels, emphasis markers into styled spans.
func inline(line *[]Span, source string, markdown bool, base int, m TextMeasurer) {
	style := base
	marked := 0
	var text strings.Builder
	i := 0
	n := len(source)
	flush := func() {
		pushIcons(line, text.String(), style, m)
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
						style = (style &^ bit) | (base & bit)
					} else {
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
}

func measureSpans(line []Span, measurer TextMeasurer, size float64) float64 {
	total := 0.0
	for k := range line {
		span := &line[k]
		if span.Icon != "" {
			span.Width = size * 1.25
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
		if len(current) > 0 && current[len(current)-1].Icon == "" {
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
		if span.Icon != "" {
			w := size * 1.25
			if width > 0 && width+w > max {
				commit()
			}
			s := span
			s.Width = w
			current = append(current, s)
			width += w
			continue
		}
		for _, piece := range rePieces.FindAllString(span.Text, -1) {
			part := piece
			if width > 0 && width+measurer.Width(js.TrimEnd(part), size, span.Style) > max {
				commit()
				part = js.TrimStart(part)
			}
			if len(current) > 0 && current[len(current)-1].Style == span.Style && current[len(current)-1].Icon == "" {
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

	if !reMarkup.MatchString(raw) && !strings.Contains(raw, "&") && !strings.Contains(raw, "ﬂ") {
		text := js.Trim(raw)
		width := measurer.Width(text, size, base)
		if width <= maxWidth || !strings.Contains(text, " ") {
			return Label{
				Lines:      [][]Span{{{Text: text, Style: base, Width: width}}},
				Widths:     []float64{width},
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

	var source string
	if markdown {
		source = js.Trim(dedent(reCRLF.ReplaceAllString(raw, "\n")))
	} else {
		source = js.Trim(raw)
	}
	var lines [][]Span
	var widths []float64
	width := 0.0
	for _, part := range reBreak.Split(source, -1) {
		var line []Span
		inline(&line, js.Trim(part), markdown, base, measurer)
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
