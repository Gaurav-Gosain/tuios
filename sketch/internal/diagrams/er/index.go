// Ported from pele src/diagrams/er/index.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package er

import "tuios.dev/sketch/internal/types"

var Er = &types.Diagram{
	Type: "er",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := NewErDb()
		if title != nil && *title != "" {
			db.title = *title
		}
		parseEr(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderEr(model.(*ErDb), config, options)
	},
}
