// Ported from pele src/text/math.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele keeps the resolver of the render in progress in module state. The port
// carries it on the measurer instead (WithMath), as it does the icons, so that
// renders can run at the same time.

package text

import (
	"math"

	"tuios.dev/sketch/internal/js"
)

// MathMarkup is a typeset $$...$$ expression: markup and its size in pixels.
// pele's MathMarkup is { svg, width, height } or { html, width, height }.
type MathMarkup struct {
	// Markup is the inside of an <svg> element, or HTML when HTML is set. It is
	// written into the SVG as it is.
	Markup string
	// HTML marks Markup as HTML, drawn in a <foreignObject>. HTML needs the
	// typesetter's CSS on the page.
	HTML bool
	// Width and Height are in pixels.
	Width  float64
	Height float64
}

// MathResolver typesets the TeX of a $$...$$ label, without the $$ delimiters,
// at a font size in pixels. It reports false when it has nothing for it.
type MathResolver func(tex string, size float64) (MathMarkup, bool)

type mathState struct {
	resolver MathResolver
	found    map[string]*MathMarkup
}

// WithMath gives a measurer that also carries the host's math resolver for one
// render. A nil resolver leaves $$...$$ as text.
func WithMath(m TextMeasurer, resolver MathResolver) TextMeasurer {
	if resolver == nil {
		return m
	}
	h := hostOf(m)
	h.math = &mathState{resolver: resolver, found: map[string]*MathMarkup{}}
	return h
}

// WithoutMath gives the measurer without its math resolver. Diagram titles are
// laid out with it: Mermaid draws them as text, whatever they hold.
func WithoutMath(m TextMeasurer) TextMeasurer {
	h, ok := m.(hosted)
	if !ok || h.math == nil {
		return m
	}
	h.math = nil
	return h
}

func mathActive(m TextMeasurer) bool {
	h, ok := m.(hosted)
	return ok && h.math != nil
}

func valid(size float64) bool {
	return !math.IsNaN(size) && !math.IsInf(size, 0) && size > 0
}

// typeset is the resolver's markup for tex at size, or nil.
func typeset(m TextMeasurer, tex string, size float64) *MathMarkup {
	h, ok := m.(hosted)
	if !ok || h.math == nil {
		return nil
	}
	key := js.Num(size) + "\n" + tex
	if markup, ok := h.math.found[key]; ok {
		return markup
	}
	var markup *MathMarkup
	if result, ok := h.math.resolver(tex, size); ok && valid(result.Width) && valid(result.Height) {
		markup = &result
	}
	h.math.found[key] = markup
	return markup
}
