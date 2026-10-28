// Ported from pele src/diagrams/er/markers.ts at 8aff183be57021b533f319a29341a529be934b08.

package er

import "tuios.dev/sketch/internal/svg"

// How far a marker reaches back along its line from the box it touches.
const MARKER_LENGTH = 18

const (
	BAR         = 5
	FOOT        = 6
	FOOT_LENGTH = 10
	RING        = 3.5
)

// markerAt is at() of erMarker: the point `back` along the line from (x, y) and `side` across it.
//
//go:noinline
func markerAt(x, y, dx, dy, back, side float64) string {
	return svg.Num(x-float64(dx*back)-float64(dy*side)) + "," + svg.Num(y-float64(dy*back)+float64(dx*side))
}

func markerBar(x, y, dx, dy, back float64) string {
	return "M" + markerAt(x, y, dx, dy, back, -BAR) + "L" + markerAt(x, y, dx, dy, back, BAR)
}

func markerRing(x, y, dx, dy, back float64) string {
	return `<circle class="pele-marker" cx="` + svg.Num(x-float64(dx*back)) + `" cy="` + svg.Num(y-float64(dy*back)) + `" r="3.5" fill="var(--_bg)"/>`
}

func markerPath(d string) string { return `<path class="pele-marker" d="` + d + `"/>` }

// Draws a crow's foot cardinality marker over the end of a line that meets a box at (x, y)
// while travelling along the unit vector (dx, dy). Types are Mermaid's lower-cased cardinalities.
func erMarker(typ string, x, y, dx, dy float64) string {
	foot := func() string {
		return "M" + markerAt(x, y, dx, dy, 0, -FOOT) + "L" + markerAt(x, y, dx, dy, FOOT_LENGTH, 0) + "L" + markerAt(x, y, dx, dy, 0, FOOT)
	}

	switch typ {
	case "only_one":
		return markerPath(markerBar(x, y, dx, dy, 6) + markerBar(x, y, dx, dy, 10))
	case "zero_or_one":
		return markerPath(markerBar(x, y, dx, dy, 6)) + markerRing(x, y, dx, dy, 13.5)
	case "one_or_more":
		return markerPath(foot() + markerBar(x, y, dx, dy, FOOT_LENGTH+3.5))
	case "zero_or_more":
		return markerPath(foot()) + markerRing(x, y, dx, dy, FOOT_LENGTH+RING+0.5)
	case "md_parent":
		return `<path class="pele-marker" d="M` + markerAt(x, y, dx, dy, 0, 0) + "L" + markerAt(x, y, dx, dy, 6, 4) + "L" + markerAt(x, y, dx, dy, 12, 0) + "L" + markerAt(x, y, dx, dy, 6, -4) + `Z" fill="var(--_bg)"/>`
	}
	return ""
}
