package main

import (
	"strconv"
	"strings"

	"tuios.dev/plot"
)

// ansi16 maps each role to an ANSI colour slot, as loom's 16-colour palette
// does, so a chart takes the colours of the terminal's own theme.
var ansi16 = map[plot.Role]int{
	plot.Fg: 15, plot.Dim: 7, plot.Mute: 8, plot.Edge: 8, plot.Track: 8,
	plot.Accent: 5, plot.AccentBright: 13,
	plot.Success: 10, plot.Error: 9, plot.Warning: 11, plot.Info: 12,
	plot.Series1: 13, plot.Series2: 12, plot.Series3: 10, plot.Series4: 11, plot.Series5: 9, plot.Series6: 14,
	plot.Heat1: 4, plot.Heat2: 12, plot.Heat3: 6, plot.Heat4: 5, plot.Heat5: 13,
}

// slot is the ANSI slot of a role, or -1 for none. A faded bright colour is
// its normal slot from fade 2, as plot.Fade asks of a 16-colour host.
func slot(r plot.Role) int {
	base, k := r.Faded()
	c, ok := ansi16[base]
	if !ok {
		return -1
	}
	if k >= 2 && c >= 9 && c <= 14 {
		c -= 8
	}
	return c
}

// sgr is the SGR sequence for a foreground slot, a background slot and bold.
func sgr(fg, bg int, bold bool) string {
	var p []string
	if bold {
		p = append(p, "1")
	}
	if fg >= 0 {
		p = append(p, strconv.Itoa(code(fg, 30, 90)))
	}
	if bg >= 0 {
		p = append(p, strconv.Itoa(code(bg, 40, 100)))
	}
	if len(p) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(p, ";") + "m"
}

func code(slot, normal, bright int) int {
	if slot >= 8 {
		return bright + slot - 8
	}
	return normal + slot
}

// render writes a grid as lines of text, with SGR colours when color is set.
func render(g *plot.Grid, color bool) string {
	if !color {
		s := g.String()
		if s == "" && g.H == 0 {
			return ""
		}
		return s + "\n"
	}
	var b strings.Builder
	for y := 0; y < g.H; y++ {
		cur := ""
		var line strings.Builder
		for x := 0; x < g.W; x++ {
			c := g.At(x, y)
			if c.Width == 0 {
				continue
			}
			text := c.Text
			if text == "" {
				text = " "
			}
			want := ""
			if c.Text != "" || c.Bg != plot.None {
				want = sgr(slot(c.Role), slot(c.Bg), c.Bold)
			}
			if want != cur {
				if cur != "" {
					line.WriteString("\x1b[m")
				}
				line.WriteString(want)
				cur = want
			}
			line.WriteString(text)
		}
		if cur != "" {
			line.WriteString("\x1b[m")
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// title is the chart's title line, bold in colour. Control characters are
// dropped, so a title cannot steer the terminal.
func title(s string, color bool) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
	if color {
		return "\x1b[1m" + s + "\x1b[m"
	}
	return s
}
