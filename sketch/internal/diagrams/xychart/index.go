// Ported from pele src/diagrams/xychart/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

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
