// Ported from pele src/diagrams/class/markers.ts at 8aff183be57021b533f319a29341a529be934b08.

package class

import "tuios.dev/sketch/internal/svg"

const (
	TRIANGLE = 12
	DIAMOND  = 16
	HALF     = 6
	RING     = 5
)

// How far the line stops short of the node so that it meets the back of the marker.
func classMarkerTrim(typ EndMarker) float64 {
	switch typ {
	case "extension":
		return TRIANGLE
	case "composition", "aggregation":
		return DIAMOND
	case "lollipop":
		return 2 * RING
	case "dependency":
		return svg.MarkerTrim("arrow_point")
	}
	return 0
}

// Draws an end marker whose tip is at (x, y), pointing along (dx, dy).
func classMarker(typ EndMarker, x, y, dx, dy float64, color string) string {
	num := svg.Num
	nx := -dy
	ny := dx
	switch typ {
	case "extension":
		bx := x - float64(dx*TRIANGLE)
		by := y - float64(dy*TRIANGLE)
		return `<path class="pele-marker" d="M` + num(x) + `,` + num(y) + `L` + num(bx+float64(nx*HALF)) + `,` + num(by+float64(ny*HALF)) + `L` +
			num(bx-float64(nx*HALF)) + `,` + num(by-float64(ny*HALF)) + `Z" stroke-linejoin="round"/>`
	case "composition", "aggregation":
		mx := x - (dx*DIAMOND)/2
		my := y - (dy*DIAMOND)/2
		half := float64(HALF - 1)
		fill := ""
		if typ == "composition" {
			fill = ` fill="` + color + `"`
		}
		return `<path class="pele-marker" d="M` + num(x) + `,` + num(y) + `L` + num(mx+float64(nx*half)) + `,` + num(my+float64(ny*half)) + `L` +
			num(x-float64(dx*DIAMOND)) + `,` + num(y-float64(dy*DIAMOND)) + `L` + num(mx-float64(nx*half)) + `,` + num(my-float64(ny*half)) + `Z" stroke-linejoin="round"` + fill + `/>`
	case "lollipop":
		return `<circle class="pele-marker" cx="` + num(x-float64(dx*RING)) + `" cy="` + num(y-float64(dy*RING)) + `" r="5"/>`
	case "dependency":
		return svg.Marker("arrow_point", x, y, dx, dy, color)
	}
	return ""
}
