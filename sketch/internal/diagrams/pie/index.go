// Ported from pele src/diagrams/pie/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package pie

import "tuios.dev/sketch/internal/types"

var Pie = &types.Diagram{
	Type: "pie",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildPie(parsePie(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderPie(model.(*PieModel), config, options)
	},
}
