// Ported from pele src/diagrams/railroad/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package railroad draws Mermaid's railroad diagrams: each rule of a grammar as a track that
// runs through its terminals and nonterminals, in Mermaid's own notation, EBNF, ABNF or PEG.
package railroad

import "tuios.dev/sketch/internal/types"

// Railroad is the railroad diagram type.
var Railroad = &types.Diagram{
	Type: "railroad",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return parseRailroad(source, types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderRailroad(model.(*RailroadDb), config, options)
	},
}
