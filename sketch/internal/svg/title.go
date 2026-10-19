// Ported from pele src/svg/title.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package svg

import (
	"math"

	"tuios.dev/sketch/internal/text"
)

type Titled struct {
	Width   float64
	Height  float64
	Content string
}

// WithTitle puts a diagram title above content that was laid out without one.
func WithTitle(title string, measurer text.TextMeasurer, size, pad, width, height float64, content string) Titled {
	label := text.LayoutLabel(title, false, measurer, size, 4000, text.Bold)
	if label.Height == 0 {
		return Titled{width, height, content}
	}
	shift := label.Height + 12
	full := math.Max(width, math.Ceil(label.Width+2*pad))
	return Titled{
		Width:  full,
		Height: math.Ceil(height + shift),
		Content: LabelSvg(label, full/2, pad+label.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) +
			`<g transform="translate(` + Num((full-width)/2) + `,` + Num(shift) + `)">` + content + `</g>`,
	}
}
