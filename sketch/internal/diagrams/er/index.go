// Ported from pele src/diagrams/er/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

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
