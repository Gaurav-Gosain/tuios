// Ported from pele src/diagrams/radar/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

// Package radar draws Mermaid's radar chart: curves of values over axes that spread
// around a centre, on a graticule of circles or polygons.
package radar

import "tuios.dev/sketch/internal/types"

// Radar is the radar chart type.
var Radar = &types.Diagram{
	Type: "radar",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildRadar(parseRadar(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderRadar(model.(*RadarModel), config, options)
	},
}
