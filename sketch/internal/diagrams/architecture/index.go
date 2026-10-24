// Ported from pele src/diagrams/architecture/index.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

// Package architecture draws Mermaid's architecture diagram: services and junctions on a
// grid, in groups, joined by edges that leave from the sides the source names.
package architecture

import "tuios.dev/sketch/internal/types"

// Architecture is the architecture diagram type.
var Architecture = &types.Diagram{
	Type: "architecture",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newArchitectureDb()
		if title != nil && *title != "" {
			db.title = *title
		}
		populate(parseArchitecture(source), db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderArchitecture(model.(*ArchitectureDb), config, options)
	},
}
