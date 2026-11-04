package main

import (
	"math"

	"github.com/charmbracelet/x/ansi"
	"github.com/egoist/mygo/ui"
	"github.com/rivo/uniseg"
)

// Box drawing (U+2500 to U+257F) and block elements (U+2580 to U+259F) are
// drawn as rectangles that fill the cell, so the lines of full-screen
// programs join from cell to cell and row to row, whatever the font's glyphs
// do at a line height above 1.

// boxLines gives each line character its four arms: left, up, right, down.
// 1 is a light arm, 2 a heavy one, 3 a double one.
var boxLines = map[rune][4]uint8{
	'─': {1, 0, 1, 0}, '━': {2, 0, 2, 0}, '│': {0, 1, 0, 1}, '┃': {0, 2, 0, 2},
	'┌': {0, 0, 1, 1}, '┐': {1, 0, 0, 1}, '└': {0, 1, 1, 0}, '┘': {1, 1, 0, 0},
	'┏': {0, 0, 2, 2}, '┓': {2, 0, 0, 2}, '┗': {0, 2, 2, 0}, '┛': {2, 2, 0, 0},
	'├': {0, 1, 1, 1}, '┤': {1, 1, 0, 1}, '┬': {1, 0, 1, 1}, '┴': {1, 1, 1, 0},
	'┼': {1, 1, 1, 1}, '┣': {0, 2, 2, 2}, '┫': {2, 2, 0, 2}, '┳': {2, 0, 2, 2},
	'┻': {2, 2, 2, 0}, '╋': {2, 2, 2, 2},
	'╭': {0, 0, 1, 1}, '╮': {1, 0, 0, 1}, '╯': {1, 1, 0, 0}, '╰': {0, 1, 1, 0},
	'═': {3, 0, 3, 0}, '║': {0, 3, 0, 3}, '╔': {0, 0, 3, 3}, '╗': {3, 0, 0, 3},
	'╚': {0, 3, 3, 0}, '╝': {3, 3, 0, 0}, '╠': {0, 3, 3, 3}, '╣': {3, 3, 0, 3},
	'╦': {3, 0, 3, 3}, '╩': {3, 3, 3, 0}, '╬': {3, 3, 3, 3},
	'╴': {1, 0, 0, 0}, '╵': {0, 1, 0, 0}, '╶': {0, 0, 1, 0}, '╷': {0, 0, 0, 1},
}

func isBox(r rune) bool {
	if _, ok := boxLines[r]; ok {
		return true
	}
	return r >= 0x2580 && r <= 0x2590 || r >= 0x2591 && r <= 0x2593
}

// drawBox draws one box or block character into its cell.
func drawBox(p *ui.Painter, r rune, cell ui.Rect, c ui.Color, scale float32) {
	px := 1 / scale
	snap := func(v float32) float32 { return float32(math.Round(float64(v*scale))) / scale }
	if arms, ok := boxLines[r]; ok {
		light := max(snap(cell.H/16), px)
		heavy := light * 2
		cx := snap(cell.X + cell.W/2 - light/2)
		cy := snap(cell.Y + cell.H/2 - light/2)
		thick := func(a uint8) float32 {
			if a == 2 {
				return heavy
			}
			return light
		}
		// A double arm is two light lines a light line apart.
		hline := func(x0, x1 float32, a uint8) {
			if a == 3 {
				p.Fill(ui.Rect{X: x0, Y: cy - light, W: x1 - x0, H: light}, c, 0)
				p.Fill(ui.Rect{X: x0, Y: cy + light, W: x1 - x0, H: light}, c, 0)
				return
			}
			t := thick(a)
			p.Fill(ui.Rect{X: x0, Y: cy + light/2 - t/2, W: x1 - x0, H: t}, c, 0)
		}
		vline := func(y0, y1 float32, a uint8) {
			if a == 3 {
				p.Fill(ui.Rect{X: cx - light, Y: y0, W: light, H: y1 - y0}, c, 0)
				p.Fill(ui.Rect{X: cx + light, Y: y0, W: light, H: y1 - y0}, c, 0)
				return
			}
			t := thick(a)
			p.Fill(ui.Rect{X: cx + light/2 - t/2, Y: y0, W: t, H: y1 - y0}, c, 0)
		}
		midX, midY := cx+light, cy+light
		if arms[0] != 0 {
			hline(cell.X, midX, arms[0])
		}
		if arms[2] != 0 {
			hline(cx, cell.X+cell.W, arms[2])
		}
		if arms[1] != 0 {
			vline(cell.Y, midY, arms[1])
		}
		if arms[3] != 0 {
			vline(cy, cell.Y+cell.H, arms[3])
		}
		return
	}
	switch {
	case r == '█':
		p.Fill(cell, c, 0)
	case r == '▀':
		p.Fill(ui.Rect{X: cell.X, Y: cell.Y, W: cell.W, H: snap(cell.H / 2)}, c, 0)
	case r >= '▁' && r <= '▇': // lower eighths
		h := snap(cell.H * float32(r-'▁'+1) / 8)
		p.Fill(ui.Rect{X: cell.X, Y: cell.Y + cell.H - h, W: cell.W, H: h}, c, 0)
	case r >= '▉' && r <= '▏': // left eighths, 7/8 down to 1/8
		w := snap(cell.W * float32('▏'-r+1) / 8)
		p.Fill(ui.Rect{X: cell.X, Y: cell.Y, W: w, H: cell.H}, c, 0)
	case r == '▐':
		p.Fill(ui.Rect{X: cell.X + snap(cell.W/2), Y: cell.Y, W: cell.W - snap(cell.W/2), H: cell.H}, c, 0)
	case r == '▌':
		p.Fill(ui.Rect{X: cell.X, Y: cell.Y, W: snap(cell.W / 2), H: cell.H}, c, 0)
	case r == '▄':
		h := snap(cell.H / 2)
		p.Fill(ui.Rect{X: cell.X, Y: cell.Y + cell.H - h, W: cell.W, H: h}, c, 0)
	case r == '░':
		p.Fill(cell, c.Alpha(0.25), 0)
	case r == '▒':
		p.Fill(cell, c.Alpha(0.5), 0)
	case r == '▓':
		p.Fill(cell, c.Alpha(0.75), 0)
	default:
		p.Fill(cell, c, 0)
	}
}

// splitClusters splits text into grapheme clusters.
func splitClusters(s string) []string {
	var out []string
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		out = append(out, g.Str())
	}
	return out
}

// runeWide reports whether a cluster takes two cells.
func runeWide(cluster string) bool { return ansi.StringWidth(cluster) == 2 }
