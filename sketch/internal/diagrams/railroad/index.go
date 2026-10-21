// Ported from pele src/diagrams/railroad/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

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
