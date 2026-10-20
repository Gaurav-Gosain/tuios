// Ported from pele src/diagrams/state/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package state

import "tuios.dev/sketch/internal/types"

var State = &types.Diagram{
	Type: "state",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := NewStateDb()
		if title != nil && *title != "" {
			db.Title = *title
		}
		parseState(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderState(model.(*StateDb), config, options)
	},
}
