// Ported from pele src/diagrams/info/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package info draws Mermaid's info diagram: the version of pele, which sketch
// ports, and an optional title.
package info

import "tuios.dev/sketch/internal/types"

// Info is the info diagram type.
var Info = &types.Diagram{
	Type: "info",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildInfo(parseInfo(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderInfo(model.(*InfoModel), config, options)
	},
}
