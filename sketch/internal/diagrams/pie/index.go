// Ported from pele src/diagrams/pie/index.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

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
