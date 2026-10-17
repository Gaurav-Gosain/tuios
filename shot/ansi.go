package shot

import (
	"fmt"
	"strings"
)

// ANSI and plain text backends. ANSI is the exact styled stream for
// terminals: cat it anywhere, pipe it into other tools, diff it. No frame,
// because a frame has no meaning in a stream. Text is for quoting and
// grepping.

// RenderANSI emits the grid as truecolor SGR text, one line per row, reset
// at every style change and at each line end so a partial paste cannot
// bleed style into the host.
func RenderANSI(g *Grid) []byte {
	var b strings.Builder
	b.Grow(g.Cols * g.Rows * 2)
	for y := 0; y < g.Rows; y++ {
		// cur is the SGR currently in effect on this line, empty for the
		// default style. Tracking the string rather than the cell is what
		// keeps an unstyled run unstyled: two cells can differ in a field the
		// stream cannot express (a background equal to the default) and still
		// need no escape between them.
		cur := ""
		pending := 0 // trailing default-style blanks held back
		emit := func(want string) {
			if want == cur {
				return
			}
			if cur != "" {
				b.WriteString("\x1b[0m")
			}
			b.WriteString(want)
			cur = want
		}
		for x := 0; x < g.Cols; x++ {
			c := g.Cells[y][x]
			if c.Width == 0 {
				continue
			}
			if c.BGDefault && c.Cluster == "" && c.Underline == UnderlineNone && !c.Strike {
				pending++
				continue
			}
			if pending > 0 {
				emit("")
				b.WriteString(strings.Repeat(" ", pending))
				pending = 0
			}
			emit(sgrFor(g, c))
			b.WriteString(displayCluster(c))
		}
		// Reset unconditionally, even on a line that ended unstyled: the
		// stream is meant to be pasted in pieces, and four bytes a line buys
		// the guarantee that no fragment can bleed style into the host.
		b.WriteString("\x1b[0m")
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// sgrFor builds the SGR prefix for one cell's style.
func sgrFor(g *Grid, c Cell) string {
	var parts []string
	if c.Bold {
		parts = append(parts, "1")
	}
	if c.Faint {
		parts = append(parts, "2")
	}
	if c.Italic {
		parts = append(parts, "3")
	}
	if c.Strike {
		parts = append(parts, "9")
	}
	switch c.Underline {
	case UnderlineSingle:
		parts = append(parts, "4")
	case UnderlineDouble:
		parts = append(parts, "4:2")
	case UnderlineCurly:
		parts = append(parts, "4:3")
	case UnderlineDotted:
		parts = append(parts, "4:4")
	case UnderlineDashed:
		parts = append(parts, "4:5")
	}
	if c.FG != g.FG {
		parts = append(parts, fmt.Sprintf("38;2;%d;%d;%d", c.FG.R, c.FG.G, c.FG.B))
	}
	if !c.BGDefault {
		parts = append(parts, fmt.Sprintf("48;2;%d;%d;%d", c.BG.R, c.BG.G, c.BG.B))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// RenderText emits the grid as plain text, styles dropped, lines
// right-trimmed.
func RenderText(g *Grid) []byte {
	var b strings.Builder
	b.Grow(g.Cols * g.Rows)
	for y := 0; y < g.Rows; y++ {
		var line strings.Builder
		for x := 0; x < g.Cols; x++ {
			c := g.Cells[y][x]
			if c.Width == 0 {
				continue
			}
			line.WriteString(displayCluster(c))
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
