// Ported from pele src/diagrams/git/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package git draws Mermaid's gitGraph: commits on branch lanes, with merges,
// cherry-picks and tags.
package git

import "tuios.dev/sketch/internal/types"

// GitGraph is the gitGraph diagram type.
var GitGraph = &types.Diagram{
	Type: "gitGraph",
	Parse: func(source string, config types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildGit(parseGit(source), config, title)
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderGit(model.(*GitModel), config, options)
	},
}
