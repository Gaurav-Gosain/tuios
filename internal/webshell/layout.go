package webshell

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The helpers here lay listings and tables out for the pane width, so a
// name or a table row never wraps in the middle. A wrapped line breaks a
// word in two and pushes the rest of the output out of line. They all
// measure with ansi.StringWidth, so their input may carry colour.

// columns lays items out top to bottom, then left to right, in as many
// columns as fit in cols, the way GNU ls does. Columns are two cells apart.
// An item wider than the pane gets a line of its own and is cut to fit.
func columns(items []string, cols int) []string {
	if len(items) == 0 {
		return nil
	}
	widths := make([]int, len(items))
	for i, it := range items {
		widths[i] = ansi.StringWidth(it)
	}
	const gap = 2
	// Try the most columns first. The widest layout that fits wins.
	for ncols := len(items); ncols >= 1; ncols-- {
		nrows := (len(items) + ncols - 1) / ncols
		ncols := (len(items) + nrows - 1) / nrows // drop empty columns
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

// table lays out rows of a key and a description, with the keys in one
// column indent cells from the left. When any description does not fit
// beside its key, every description goes on the lines below its key,
// indented two more cells and wrapped at word boundaries. Deciding once
// for the table keeps its rows alike.
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

// wrapWords wraps s at spaces into lines at most w cells wide.
func wrapWords(s string, w int) []string { return wrapTokens(strings.Fields(s), w) }

// wrapTokens joins tokens with single spaces into lines at most w cells
// wide. A token never breaks across lines. One wider than w is cut to fit.
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

// fit cuts s to at most cols cells, ending in "…" when it cuts.
func fit(s string, cols int) string {
	if ansi.StringWidth(s) <= cols {
		return s
	}
	return ansi.Truncate(s, max(cols, 1), "…")
}

// printLines prints each line on a line of its own.
func printLines(t *TTY, lines []string) {
	for _, l := range lines {
		t.Print(l + "\r\n")
	}
}
