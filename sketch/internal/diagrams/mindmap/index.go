// Ported from pele src/diagrams/mindmap/index.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package mindmap

import "tuios.dev/sketch/internal/types"

var Mindmap = &types.Diagram{
	Type: "mindmap",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := &MindmapDb{}
		db.title = types.Or(title, "")
		parseMindmap(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderMindmap(model.(*MindmapDb), config, options)
	},
}
