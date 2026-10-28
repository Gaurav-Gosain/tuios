// Ported from pele src/diagrams/block/arrows.ts at 8aff183be57021b533f319a29341a529be934b08.

package block

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
)

const (
	RIGHT = 1
	LEFT  = 2
	UP    = 4
	DOWN  = 8
)

// OVERHANG is how far a head reaches past each side of its shaft.
const OVERHANG = 7
const MAX_DEPTH = 20
const NUB = 12

type ArrowSize struct {
	w float64
	h float64
	// Shift of the label from the center, away from the heads.
	dx float64
	dy float64
}

// arrowMask: Mermaid compares directions exactly, so `( up )` draws no arrow there. Here spaces are ignored.
func arrowMask(directions []string) int {
	mask := 0
	for _, direction := range directions {
		switch js.Trim(direction) {
		case "right":
			mask |= RIGHT
		case "left":
			mask |= LEFT
		case "up":
			mask |= UP
		case "down":
			mask |= DOWN
		case "x":
			mask |= RIGHT | LEFT
		case "y":
			mask |= UP | DOWN
		}
	}
	return mask
}

func bit(mask, flag int) float64 {
	if mask&flag != 0 {
		return 1
	}
	return 0
}

// arrowAxis: an arrow along one axis has a shaft and heads wider than it. One that points along both axes
// is a box with a small point on each side it names.
func arrowAxis(mask int) byte {
	if mask != 0 && mask&(UP|DOWN) == 0 {
		return 'x'
	}
	if mask != 0 && mask&(RIGHT|LEFT) == 0 {
		return 'y'
	}
	return 0
}

func arrowSize(mask int, tw, th float64) ArrowSize {
	axis := arrowAxis(mask)
	across := bit(mask, LEFT) - bit(mask, RIGHT)
	along := bit(mask, UP) - bit(mask, DOWN)
	if axis == 'x' {
		h := math.Max(th+8, 14) + 2*OVERHANG
		depth := math.Min(h/2, MAX_DEPTH)
		heads := 1.0
		if mask == RIGHT|LEFT {
			heads = 2
		}
		return ArrowSize{w: math.Max(tw+12, 16) + float64(depth*heads), h: h, dx: (across * depth) / 2, dy: 0}
	}
	if axis == 'y' {
		w := math.Max(tw+16, 14) + 2*OVERHANG
		depth := math.Min(w/2, MAX_DEPTH)
		heads := 1.0
		if mask == UP|DOWN {
			heads = 2
		}
		return ArrowSize{w: w, h: math.Max(th+8, 16) + float64(depth*heads), dx: 0, dy: (along * depth) / 2}
	}
	return ArrowSize{
		w:  math.Max(tw+16, 24) + float64(NUB*(bit(mask, LEFT)+bit(mask, RIGHT))),
		h:  math.Max(th+8, 24) + float64(NUB*(bit(mask, UP)+bit(mask, DOWN))),
		dx: (across * NUB) / 2,
		dy: (along * NUB) / 2,
	}
}

func drawArrow(mask int, w, h float64, attrs string) string {
	axis := arrowAxis(mask)
	x := w / 2
	y := h / 2
	var p []float64
	if axis == 'x' {
		depth := math.Min(y, MAX_DEPTH)
		s := y - OVERHANG
		x0 := -x
		if mask&LEFT != 0 {
			x0 = -x + depth
		}
		x1 := x
		if mask&RIGHT != 0 {
			x1 = x - depth
		}
		p = append(make([]float64, 0, 16), x0, -s, x1, -s)
		if mask&RIGHT != 0 {
			p = append(p, x1, -y, x, 0, x1, y)
		}
		p = append(p, x1, s, x0, s)
		if mask&LEFT != 0 {
			p = append(p, x0, y, -x, 0, x0, -y)
		}
	} else if axis == 'y' {
		depth := math.Min(x, MAX_DEPTH)
		s := x - OVERHANG
		y0 := -y
		if mask&UP != 0 {
			y0 = -y + depth
		}
		y1 := y
		if mask&DOWN != 0 {
			y1 = y - depth
		}
		p = append(make([]float64, 0, 16), s, y0, s, y1)
		if mask&DOWN != 0 {
			p = append(p, x, y1, 0, y, -x, y1)
		}
		p = append(p, -s, y1, -s, y0)
		if mask&UP != 0 {
			p = append(p, -x, y0, 0, -y, x, y0)
		}
	} else {
		x0 := -x
		if mask&LEFT != 0 {
			x0 = -x + NUB
		}
		x1 := x
		if mask&RIGHT != 0 {
			x1 = x - NUB
		}
		y0 := -y
		if mask&UP != 0 {
			y0 = -y + NUB
		}
		y1 := y
		if mask&DOWN != 0 {
			y1 = y - NUB
		}
		cx := (x0 + x1) / 2
		cy := (y0 + y1) / 2
		kx := math.Min(10, (x1-x0)/2)
		ky := math.Min(10, (y1-y0)/2)
		p = append(make([]float64, 0, 32), x0, y0)
		if mask&UP != 0 {
			p = append(p, cx-kx, y0, cx, -y, cx+kx, y0)
		}
		p = append(p, x1, y0)
		if mask&RIGHT != 0 {
			p = append(p, x1, cy-ky, x, cy, x1, cy+ky)
		}
		p = append(p, x1, y1)
		if mask&DOWN != 0 {
			p = append(p, cx+kx, y1, cx, y, cx-kx, y1)
		}
		p = append(p, x0, y1)
		if mask&LEFT != 0 {
			p = append(p, x0, cy+ky, -x, cy, x0, cy-ky)
		}
	}
	var b strings.Builder
	b.WriteString(`<polygon points="`)
	for i := 0; i < len(p); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		svg.WriteNum(&b, p[i])
		b.WriteByte(',')
		svg.WriteNum(&b, p[i+1])
	}
	svg.WriteStrings(&b, `" stroke-linejoin="round"`, attrs, `/>`)
	return b.String()
}
