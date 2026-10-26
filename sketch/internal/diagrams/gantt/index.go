// Ported from pele src/diagrams/gantt/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package gantt

import "tuios.dev/sketch/internal/types"

var Gantt = &types.Diagram{
	Type: "gantt",
	Parse: func(source string, config types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := NewGanttDb(GanttDbOptions{})
		if title != nil && *title != "" {
			db.title = *title
		}
		if conf, ok := config["gantt"].(map[string]any); ok {
			if mode, ok := conf["displayMode"].(string); ok {
				db.displayMode = mode
			}
		}
		parseGantt(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderGantt(model.(*GanttDb), config, options)
	},
}
