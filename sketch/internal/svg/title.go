// Ported from pele src/svg/title.ts at 8aff183be57021b533f319a29341a529be934b08.

package svg

import (
	"math"

	"tuios.dev/sketch/internal/text"
)

// TitleLabel lays out a diagram title. Mermaid draws diagram titles as text, whatever they hold.
func TitleLabel(title string, measurer text.TextMeasurer, size, maxWidth float64) text.Label {
	return text.LayoutLabel(title, false, text.WithoutMath(measurer), size, maxWidth, text.Bold)
}

type Titled struct {
	Width  float64
	Height float64
	// Content is the drawing in parts, to be joined in order.
	Content []string
}

// WithTitle puts a diagram title above content that was laid out without one.
func WithTitle(title string, measurer text.TextMeasurer, size, pad, width, height float64, content ...string) Titled {
	label := TitleLabel(title, measurer, size, 4000)
	if label.Height == 0 {
		return Titled{width, height, content}
	}
	shift := label.Height + 12
	full := math.Max(width, math.Ceil(label.Width+float64(2*pad)))
	return Titled{
		Width:  full,
		Height: math.Ceil(height + shift),
		Content: append(append([]string{LabelSvg(label, full/2, pad+label.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) +
			`<g transform="translate(` + Num((full-width)/2) + `,` + Num(shift) + `)">`}, content...), `</g>`),
	}
}
