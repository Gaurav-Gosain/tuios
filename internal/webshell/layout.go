package webshell

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func columns(items []string, cols int) []string {
	if len(items) == 0 {
		return nil
	}
	widths := make([]int, len(items))
	for i, it := range items {
		widths[i] = ansi.StringWidth(it)
	}
	const gap = 2
	for ncols := len(items); ncols >= 1; ncols-- {
		nrows := (len(items) + ncols - 1) / ncols
		ncols := (len(items) + nrows - 1) / nrows
		colW := make([]int, ncols)
		for i, w := range widths {
			colW[i/nrows] = max(colW[i/nrows], w)
		}
		total := gap * (ncols - 1)
		for _, w := range colW {
			total += w
		}
		if total > cols && ncols > 1 {
			continue
		}
		lines := make([]string, nrows)
		for r := range nrows {
			var b strings.Builder
			for c := range ncols {
				i := c*nrows + r
				if i >= len(items) {
					break
				}
				if c > 0 {
					b.WriteString(strings.Repeat(" ", gap))
				}
				b.WriteString(items[i])
				if next := (c+1)*nrows + r; next < len(items) {
					b.WriteString(strings.Repeat(" ", colW[c]-widths[i]))
				}
			}
			lines[r] = fit(b.String(), cols)
		}
		return lines
	}
	return nil
}

func table(rows [][2]string, indent int, keyColour string, cols int) []string {
	keyW := 0
	for _, r := range rows {
		keyW = max(keyW, ansi.StringWidth(r[0]))
	}
	descAt := indent + keyW + 2
	beside := true
	for _, r := range rows {
		if r[1] != "" && descAt+ansi.StringWidth(r[1]) > cols {
			beside = false
		}
	}
	pad := strings.Repeat(" ", indent)
	var out []string
	for _, r := range rows {
		key := pad + keyColour + r[0] + reset
		if r[1] == "" {
			out = append(out, fit(key, cols))
			continue
		}
		if beside {
			out = append(out, key+strings.Repeat(" ", descAt-indent-ansi.StringWidth(r[0]))+r[1])
			continue
		}
		out = append(out, fit(key, cols))
		under := strings.Repeat(" ", indent+2)
		for _, l := range wrapWords(r[1], cols-indent-2) {
			out = append(out, under+l)
		}
	}
	return out
}

func wrapWords(s string, w int) []string { return wrapTokens(strings.Fields(s), w) }

func wrapTokens(tokens []string, w int) []string {
	w = max(w, 1)
	var lines []string
	var line string
	lineW := 0
	for _, tok := range tokens {
		tw := ansi.StringWidth(tok)
		if lineW > 0 && lineW+1+tw > w {
			lines = append(lines, line)
			line, lineW = "", 0
		}
		if lineW > 0 {
			line += " "
			lineW++
		}
		line += fit(tok, w)
		lineW += min(tw, w)
	}
	if lineW > 0 {
		lines = append(lines, line)
	}
	return lines
}

func fit(s string, cols int) string {
	if ansi.StringWidth(s) <= cols {
		return s
	}
	return ansi.Truncate(s, max(cols, 1), "…")
}

func fitLines(lines []string, cols int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = fit(l, cols)
	}
	return out
}
