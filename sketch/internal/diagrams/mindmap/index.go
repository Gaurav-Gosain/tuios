// Ported from pele src/diagrams/mindmap/index.ts at 8aff183be57021b533f319a29341a529be934b08.

package mindmap

import "tuios.dev/sketch/internal/types"

var Mindmap = &types.Diagram{
	Type: "mindmap",
	Math: true,
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
