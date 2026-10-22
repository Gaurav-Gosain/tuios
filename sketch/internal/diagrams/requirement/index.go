// Ported from pele src/diagrams/requirement/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

// Package requirement draws Mermaid's requirement diagram: requirements and elements as
// boxes of fields, joined by their relationships.
package requirement

import "tuios.dev/sketch/internal/types"

// RequirementDiagram is the requirement diagram type.
var RequirementDiagram = &types.Diagram{
	Type: "requirement",
	Math: true,
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newRequirementDb()
		if title != nil && *title != "" {
			db.title = *title
		}
		parseRequirement(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderRequirement(model.(*RequirementDb), config, options)
	},
}
