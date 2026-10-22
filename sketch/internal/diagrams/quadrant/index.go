// Ported from pele src/diagrams/quadrant/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
