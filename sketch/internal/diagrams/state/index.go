// Ported from pele src/diagrams/state/index.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package state

import "tuios.dev/sketch/internal/types"

var State = &types.Diagram{
	Type: "state",
	Math: true,
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
