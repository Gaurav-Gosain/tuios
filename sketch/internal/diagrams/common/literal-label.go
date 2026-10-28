// Ported from pele src/diagrams/common/literal-label.ts at 8aff183be57021b533f319a29341a529be934b08.

package common

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
)

// LiteralLabel: literal labels, including filenames and protocol fields, must retain every character but the
// spaces a line breaks at. Long words can break between code points; measurement stays bounded
// by one line of text.
func LiteralLabel(txt string, measurer text.TextMeasurer, size, room float64, style int) text.Label {
	var lines []string
	for _, raw := range strings.Split(txt, "\n") {
		chars := []rune(raw)
		if len(chars) == 0 {
			lines = append(lines, "")
		}
		from := 0
		for from < len(chars) {
			fits := func(length int) bool {
				return measurer.Width(string(chars[from:from+length]), size, style) <= room
			}
			hi := 1
			for hi < len(chars)-from && fits(hi) {
				hi = min(len(chars)-from, hi*2)
			}
			lo := 1
			for lo < hi {
				mid := (lo + hi + 1) / 2
				if fits(mid) {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			take := lo
			wraps := from+take < len(chars)
			if wraps && !js.IsSpace(chars[from+take]) {
				for i := take - 1; i > 0; i-- {
					if js.IsSpace(chars[from+i]) {
						take = i + 1
						break
					}
				}
			}
			line := string(chars[from : from+take])
			if wraps {
				if t := js.TrimEnd(line); t != "" {
					line = t
				}
			}
			lines = append(lines, line)
			from += take
			for wraps && from < len(chars) && js.IsSpace(chars[from]) {
				from++
			}
		}
	}
	if txt == "" {
		lines = nil
	}
	widths := make([]float64, len(lines))
	width := 0.0
	out := make([][]text.Span, len(lines))
	for i, line := range lines {
		widths[i] = measurer.Width(line, size, style)
		width = math.Max(width, widths[i])
		out[i] = []text.Span{{Text: line, Style: style, Width: widths[i]}}
	}
	lineHeight := js.Round(size * 1.5)
	return text.Label{Lines: out, Widths: widths, Width: width, Height: float64(len(lines)) * lineHeight, Size: size, LineHeight: lineHeight}
}

func BreakLabel(label text.Label, measurer text.TextMeasurer, room float64) text.Label {
	if label.Width <= room {
		return label
	}
	var lines [][]text.Span
	var widths []float64
	for i := range label.Lines {
		if label.Widths[i] <= room {
			lines = append(lines, label.Lines[i])
			widths = append(widths, label.Widths[i])
			continue
		}
		var current []text.Span
		width := 0.0
		commit := func() {
			lines = append(lines, current)
			widths = append(widths, width)
			current = nil
			width = 0
		}
		for _, span := range label.Lines[i] {
			if span.Icon != "" || span.Math != nil {
				if len(current) > 0 && width+span.Width > room {
					commit()
				}
				current = append(current, span)
				width += span.Width
				continue
			}
			for _, ch := range span.Text {
				char := string(ch)
				var last *text.Span
				if len(current) > 0 {
					last = &current[len(current)-1]
				}
				joined := last != nil && last.Icon == "" && last.Math == nil && last.Style == span.Style
				var nextWidth float64
				if joined {
					nextWidth = measurer.Width(last.Text+char, label.Size, span.Style)
				} else {
					nextWidth = measurer.Width(char, label.Size, span.Style)
				}
				added := nextWidth
				if joined {
					added = nextWidth - last.Width
				}
				if len(current) > 0 && width+added > room {
					carry, carried := splitAtBreak(current, measurer, label.Size)
					if carried {
						width = 0
						for _, s := range current {
							width += s.Width
						}
					}
					commit()
					last = nil
					added = measurer.Width(char, label.Size, span.Style)
					if carried {
						current = append(current, carry)
						width = carry.Width
						if carry.Style == span.Style {
							last = &current[0]
							added = measurer.Width(last.Text+char, label.Size, span.Style) - last.Width
						}
						if width+added > room {
							commit()
							last = nil
							added = measurer.Width(char, label.Size, span.Style)
						}
					}
				}
				if last != nil && last.Icon == "" && last.Math == nil && last.Style == span.Style {
					last.Text += char
					last.Width += added
				} else {
					current = append(current, text.Span{Text: char, Style: span.Style, Width: added})
				}
				width += added
			}
		}
		if len(current) > 0 || len(label.Lines[i]) == 0 {
			commit()
		}
	}
	width := 0.0
	for _, w := range widths {
		width = math.Max(width, w)
	}
	out := label
	out.Lines = lines
	out.Widths = widths
	out.Width = width
	out.Height = float64(len(lines)) * label.LineHeight
	if label.Heights != nil {
		return text.WithHeights(out)
	}
	return out
}

// isBreakHyphen reports a hyphen a line may break after. U+2011, the
// non-breaking hyphen, is not one.
func isBreakHyphen(r rune) bool { return r == '-' || r == '\u2010' }

// splitAtBreak cuts the line's last span at its last space or at its last
// hyphen that follows a letter or digit, so a line too wide for its room
// breaks there rather than inside a word (tuios issue 1032; pele breaks after
// the last character that fits). A hyphen after a space or at the start, as
// in a flag or a minus sign, is not a break. The hyphen stays, the space is
// dropped, and the rest comes back to start the next line. It reports false
// when the span is an icon or math, has no break, or ends on one.
func splitAtBreak(line []text.Span, measurer text.TextMeasurer, size float64) (text.Span, bool) {
	last := &line[len(line)-1]
	if last.Icon != "" || last.Math != nil {
		return text.Span{}, false
	}
	s := last.Text
	for i := len(s); i > 0; {
		r, n := utf8.DecodeLastRuneInString(s[:i])
		i -= n
		var head, tail string
		switch {
		case isBreakHyphen(r):
			if prev, _ := utf8.DecodeLastRuneInString(s[:i]); !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
				continue
			}
			head, tail = s[:i+n], s[i+n:]
		case js.IsSpace(r):
			head, tail = js.TrimEnd(s[:i]), js.TrimStart(s[i:])
		default:
			continue
		}
		if head == "" || tail == "" {
			return text.Span{}, false
		}
		last.Text = head
		last.Width = measurer.Width(head, size, last.Style)
		return text.Span{Text: tail, Style: last.Style, Width: measurer.Width(tail, size, last.Style)}, true
	}
	return text.Span{}, false
}

// LeftLabelSvg writes a label with every line starting at x. A line is anchored at its start and keeps its
// spaces, so that where it begins does not depend on how its text was measured.
func LeftLabelSvg(label text.Label, x, top float64, attrs string) string {
	shared := svg.SharedStyle(label)
	open := "<text" + svg.TextStyle(attrs, shared)
	var body strings.Builder
	for i, spans := range label.Lines {
		cy := top + float64((float64(i)+0.5)*label.LineHeight)
		hasIcon := false
		for _, span := range spans {
			if span.Icon != "" {
				hasIcon = true
			}
		}
		if hasIcon {
			width := label.Widths[i]
			one := label
			one.Lines = [][]text.Span{spans}
			one.Widths = []float64{width}
			one.Width = width
			one.Height = label.LineHeight
			body.WriteString(svg.LabelSvg(one, x+width/2, cy, attrs, nil))
			continue
		}
		t := ""
		for _, span := range spans {
			own := svg.SpanStyle(span.Style &^ shared)
			if own != "" {
				t += `<tspan` + own + `>` + svg.Esc(span.Text) + `</tspan>`
			} else {
				t += svg.Esc(span.Text)
			}
		}
		if t != "" {
			body.WriteString(open + ` x="` + svg.Num(x) + `" y="` + svg.Num(cy+float64(label.Size*0.35)) + `" text-anchor="start" xml:space="preserve">` + t + `</text>`)
		}
	}
	return body.String()
}
