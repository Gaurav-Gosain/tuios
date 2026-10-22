// Ported from pele src/diagrams/sankey/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
		parseSankey(prepareTextForParsing(source), db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderSankey(model.(*SankeyDb), config, options)
	},
}
