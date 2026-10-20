// Ported from pele src/diagrams/flowchart/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package flowchart

import "tuios.dev/sketch/internal/types"

var Flowchart = &types.Diagram{
	Type: "flowchart",
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
		return renderFlowchart(model.(*FlowDb), config, options, nil)
	},
}
