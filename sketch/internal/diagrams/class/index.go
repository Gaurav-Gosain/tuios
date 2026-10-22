// Ported from pele src/diagrams/class/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

package class

import "tuios.dev/sketch/internal/types"

var Class = &types.Diagram{
	Type: "class",
	Math: true,
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := NewClassDb()
		if title != nil && *title != "" {
			db.Title = *title
		}
		parseClassDiagram(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderClass(model.(*ClassDb), config, options)
	},
}
