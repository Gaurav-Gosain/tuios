// Ported from pele src/diagrams/common/icon.ts at 8aff183be57021b533f319a29341a529be934b08.

package common

import (
	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
)

// IconSvg is a square for a named icon, in the form labels use. The host's resolver supplies the drawing, if any.
func IconSvg(name string, x, y, side float64, icons text.IconResolver) string {
	inner := ""
	if icons != nil {
		inner = icons(name)
	}
	return `<svg class="pele-icon" data-icon="` + svg.EscText(name) + `" x="` + svg.Num(x) + `" y="` + svg.Num(y) + `" width="` + svg.Num(side) + `" height="` + svg.Num(
		side,
	) + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
		inner +
		`</svg>`
}
