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
