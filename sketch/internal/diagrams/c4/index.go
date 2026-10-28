// Ported from pele src/diagrams/c4/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package c4 draws Mermaid's C4 diagrams: people, systems, containers and components in
// boundaries on a grid, joined by relations routed around the shapes.
package c4

import "tuios.dev/sketch/internal/types"

// C4 is the C4 diagram type.
var C4 = &types.Diagram{
	Type: "c4",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newC4Db()
		if title != nil && *title != "" {
			db.title = *title
		}
		parseC4(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderC4(model.(*C4Db), config, options)
	},
}
