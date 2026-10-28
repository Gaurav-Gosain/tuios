// Ported from pele src/diagrams/xychart/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package xychart draws Mermaid's xychart: bar and line series over a category or
// numeric axis.
package xychart

import "tuios.dev/sketch/internal/types"

// XyChart is the xychart diagram type.
var XyChart = &types.Diagram{
	Type:    "xychart",
	Section: "xyChart",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newXyChartDb()
		db.title = types.Or(title, "")
		parseXyChart(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderXyChart(model.(*XyChartDb), config, options)
	},
}
