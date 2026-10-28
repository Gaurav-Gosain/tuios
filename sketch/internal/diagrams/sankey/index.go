// Ported from pele src/diagrams/sankey/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package sankey draws Mermaid's sankey diagram: flows between nodes as bands as wide as
// their values, with the nodes in columns.
package sankey

import "tuios.dev/sketch/internal/types"

// Sankey is the sankey diagram type.
var Sankey = &types.Diagram{
	Type: "sankey",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newSankeyDb()
		if title != nil && *title != "" {
			db.title = *title
		}
		parseSankey(prepareTextForParsing(source, nil), db, source)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderSankey(model.(*SankeyDb), config, options)
	},
}
