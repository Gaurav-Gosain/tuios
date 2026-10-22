// Ported from pele src/diagrams/pie/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
