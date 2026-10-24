// Ported from pele src/diagrams/common/icon.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

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
