// Ported from pele src/diagrams/venn/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

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
