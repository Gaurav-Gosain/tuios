// Ported from pele src/diagrams/info/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

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
