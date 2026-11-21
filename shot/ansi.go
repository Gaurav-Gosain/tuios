package shot

import (
	"fmt"
	"strings"
)

// RenderANSI emits the grid as truecolor SGR text, one line per row, reset
// at every style change and at each line end so a partial paste cannot
// bleed style into the host.
func RenderANSI(g *Grid) []byte {
	var b strings.Builder
	b.Grow(g.Cols * g.Rows * 2)
	for y := 0; y < g.Rows; y++ {
		cur := ""
		pending := 0
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
		b.WriteString("\x1b[0m")
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

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
