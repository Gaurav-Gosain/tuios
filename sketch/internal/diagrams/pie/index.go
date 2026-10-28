// Ported from pele src/diagrams/pie/index.ts at 8aff183be57021b533f319a29341a529be934b08.

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
