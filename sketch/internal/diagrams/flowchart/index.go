// Ported from pele src/diagrams/flowchart/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

package flowchart

import "tuios.dev/sketch/internal/types"

var Flowchart = &types.Diagram{
	Type: "flowchart",
	Math: true,
	Parse: func(source string, config types.Config, title *string, _ int, limits types.ParseLimits) any {
		flow, _ := config["flowchart"].(map[string]any)
		inherit, _ := flow["inheritDir"].(bool)
		db := NewFlowDb(FlowDbOptions{InheritDir: inherit, MaxEdges: limits.MaxEdges})
		if title != nil && *title != "" {
			db.Title = *title
		}
		parseFlowchart(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return RenderFlowchart(model.(*FlowDb), config, options, nil)
	},
}
