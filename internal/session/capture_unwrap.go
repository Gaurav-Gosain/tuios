package session

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

func unwrapRows(term vt.Terminal, lines []string, first int, main bool) []string {
	if term == nil || len(lines) < 2 {
		return lines
	}
	sbLen, width := term.ScrollbackLen(), term.Width()
	alt := term.ActiveScreenIsAlt()
	wrapped := func(abs int) (bool, bool) {
		if abs < sbLen {
			if alt && !main && abs == sbLen-1 {
				return false, false
			}
			w, known := term.ScrollbackSoftWrapped(abs)
			return known && w, term.ScrollbackPadded(abs)
		}
		if alt && main {
			return false, false
		}
		w, known := term.RowSoftWrapped(abs - sbLen)
		return known && w, term.RowPadded(abs - sbLen)
	}
	out := make([]string, 0, len(lines))
	var cur strings.Builder
	for i, line := range lines {
		cur.WriteString(line)
		if i < len(lines)-1 {
			if w, padded := wrapped(first + i); w {
				fill := width - ansi.StringWidth(line)
				if padded {
					fill--
				}
				if fill > 0 {
					cur.WriteString(strings.Repeat(" ", fill))
				}
				continue
			}
		}
		out = append(out, cur.String())
		cur.Reset()
	}
	return out
}

func unwrapCapture(term vt.Terminal, content string, history bool) string {
	if term == nil {
		return content
	}
	lines := strings.Split(content, "\n")
	first := term.ScrollbackLen()
	if history {
		first = 0
	}
	if len(lines) != term.ScrollbackLen()-first+term.Height() {
		return content
	}
	return strings.Join(unwrapRows(term, lines, first, false), "\n")
}

func trimCapture(content string) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
