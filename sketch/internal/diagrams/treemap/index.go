// Ported from pele src/diagrams/treemap/index.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

// Package treemap draws Mermaid's treemap: sections and leaves as nested rectangles,
// each as large as its value, laid out as a squarified treemap.
package treemap

import "tuios.dev/sketch/internal/types"

// Treemap is the treemap diagram type.
var Treemap = &types.Diagram{
	Type: "treemap",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildTreemap(parseTreemap(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderTreemap(model.(*TreemapModel), config, options)
	},
}
