// Ported from pele src/diagrams/railroad/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
