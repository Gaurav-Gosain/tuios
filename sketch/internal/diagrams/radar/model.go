// Ported from pele src/diagrams/radar/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package radar

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

func decode(s string) (rune, int) { return utf8.DecodeRuneInString(s) }

type RadarAxis struct {
	name  string
	label string
}

type RadarCurve struct {
	name    string
	label   string
	entries []float64
}

type RadarOptions struct {
	showLegend bool
	ticks      float64
	// max is NaN for null: the largest value of the curves.
	max       float64
	hasMax    bool
	min       float64
	graticule string
}

type RadarModel struct {
	title    string
	accTitle string
	accDescr string
	axes     []RadarAxis
	curves   []RadarCurve
	options  RadarOptions
}

const MAX_TICKS = 32

func invalid(message string) {
	types.Throw(message, types.CodeSemantic, types.ErrorInfo{Type: "radar"})
}

// joinLines is .replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}

func buildRadar(ast *RadarAst, title string) *RadarModel {
	axes := make([]RadarAxis, len(ast.axes))
	for i, axis := range ast.axes {
		axes[i] = RadarAxis{name: axis.name, label: axis.name}
		if axis.hasLabel {
			axes[i].label = axis.label
		}
	}

	curves := make([]RadarCurve, len(ast.curves))
	for c, curve := range ast.curves {
		var entries []float64
		if !curve.entries[0].hasAxis {
			entries = make([]float64, len(curve.entries))
			for i, entry := range curve.entries {
				entries[i] = entry.value
			}
		} else {
			if len(axes) == 0 {
				invalid("Axes must be populated before curves for reference entries")
			}
			byAxis := map[string]float64{}
			for _, entry := range curve.entries {
				if _, ok := byAxis[entry.axis]; entry.hasAxis && !ok {
					byAxis[entry.axis] = entry.value
				}
			}
			entries = make([]float64, len(axes))
			for i, axis := range axes {
				value, ok := byAxis[axis.name]
				if !ok {
					invalid("Missing entry for axis " + axis.label)
				}
				entries[i] = value
			}
		}
		curves[c] = RadarCurve{name: curve.name, label: curve.name, entries: entries}
		if curve.hasLabel {
			curves[c].label = curve.label
		}
	}

	options := RadarOptions{showLegend: true, ticks: 5, max: math.NaN(), min: 0, graticule: "circle"}
	for _, option := range ast.options {
		switch option.name {
		case "showLegend":
			options.showLegend = option.boolean
		case "graticule":
			options.graticule = option.text
		case "ticks":
			options.ticks = option.number
		case "max":
			options.max, options.hasMax = option.number, true
		case "min":
			options.min = option.number
		}
	}
	if options.ticks > MAX_TICKS {
		options.ticks = MAX_TICKS
	}

	model := &RadarModel{title: ast.title, axes: axes, curves: curves, options: options}
	if model.title == "" {
		model.title = title
	}
	if ast.accTitle != "" {
		model.accTitle = js.TrimStart(ast.accTitle)
	}
	if ast.accDescr != "" {
		model.accDescr = joinLines(ast.accDescr)
	}
	return model
}
