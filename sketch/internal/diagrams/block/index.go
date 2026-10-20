// Ported from pele src/diagrams/block/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

// Package block draws Mermaid's block diagram: blocks in a grid of columns, nested
// in composite blocks, with edges between them.
package block

import "tuios.dev/sketch/internal/types"

// BlockDiagram is the block diagram type.
var BlockDiagram = &types.Diagram{
	Type: "block",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := newBlockDb()
		db.title = types.Or(title, "")
		parseBlock(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderBlock(model.(*BlockDb), config, options)
	},
}
