// Ported from pele src/diagrams/radar/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

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
