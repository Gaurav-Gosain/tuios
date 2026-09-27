package fang

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// RenderErrorText renders s in style, wrapping it at the style's width only
// between words.
//
// lipgloss wraps a word longer than the width by cutting it, and the words
// that run long in an error are paths, socket names and commands. A path cut
// in two cannot be copied back out of the terminal, and a test or a person
// searching the output for it does not find it. A word longer than the width
// here stays whole on a line of its own and the terminal soft-wraps it, which
// copies back as one piece.
//
// Each line is rendered on its own. lipgloss pads every line of a block to the
// block's widest, so one long path would pad every other line past the
// terminal's edge too, and each would wrap onto a blank row.
func RenderErrorText(style lipgloss.Style, s string) string {
	// A transform, such as fang's capital on the first word, is for the text
	// as a whole, not for each line it wraps onto.
	if transform := style.GetTransform(); transform != nil {
		s = transform(s)
		style = style.UnsetTransform()
	}
	limit := style.GetWidth() - style.GetHorizontalPadding() - style.GetHorizontalBorderSize()
	if limit > 0 {
		s = wrapWords(s, limit)
	}
	style = style.UnsetWidth()
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// wrapWords wraps every line of s to limit cells, breaking only at spaces. A
// line's leading indent is kept on the lines it wraps onto.
func wrapWords(s string, limit int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = wrapLine(line, limit)
	}
	return strings.Join(lines, "\n")
}

func wrapLine(line string, limit int) string {
	if ansi.StringWidth(line) <= limit {
		return line
	}
	body := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(body)]
	var out strings.Builder
	out.WriteString(indent)
	width := ansi.StringWidth(indent)
	start := true
	for word := range strings.FieldsSeq(body) {
		w := ansi.StringWidth(word)
		if !start && width+1+w > limit {
			out.WriteString("\n")
			out.WriteString(indent)
			width = ansi.StringWidth(indent)
			start = true
		}
		if !start {
			out.WriteString(" ")
			width++
		}
		out.WriteString(word)
		width += w
		start = false
	}
	return out.String()
}
