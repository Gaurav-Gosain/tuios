// Ported from pele src/diagrams/quadrant/index.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

// Package quadrant draws Mermaid's quadrantChart: points on a square of four
// quadrants, with labelled axes.
package quadrant

import "tuios.dev/sketch/internal/types"

// QuadrantChart is the quadrantChart diagram type.
var QuadrantChart = &types.Diagram{
	Type: "quadrantChart",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newQuadrantDb()
		db.title = types.Or(title, "")
		parseQuadrant(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderQuadrant(model.(*QuadrantDb), config, options)
	},
}
