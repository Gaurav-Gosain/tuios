// Ported from pele src/diagrams/venn/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package venn draws Mermaid's venn diagram: a circle for each set, sized by the set, placed so
// that the circles overlap as much as the overlaps stated, with labels and texts in each region.
package venn

import "tuios.dev/sketch/internal/types"

// Venn is the venn diagram type.
var Venn = &types.Diagram{
	Type: "venn",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newVennDb()
		if title != nil && *title != "" {
			db.title, db.hasTitle = *title, true
		}
		parseVenn(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderVenn(model.(*VennDb), config, options)
	},
}
