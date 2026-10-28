// Ported from pele src/diagrams/ishikawa/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package ishikawa draws Mermaid's ishikawa diagram: a fishbone of causes that lead to an
// effect, the categories of causes as bones off a spine.
package ishikawa

import "tuios.dev/sketch/internal/types"

// Ishikawa is the ishikawa diagram type.
var Ishikawa = &types.Diagram{
	Type: "ishikawa",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := &IshikawaDb{title: types.Or(title, "")}
		parseIshikawa(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderIshikawa(model.(*IshikawaDb), config, options)
	},
}
