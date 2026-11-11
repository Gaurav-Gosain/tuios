// Ported from pele src/diagrams/state/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

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
