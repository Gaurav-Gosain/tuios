// Ported from pele src/diagrams/flowchart/index.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

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
