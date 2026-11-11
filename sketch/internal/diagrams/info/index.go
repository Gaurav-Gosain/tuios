// Ported from pele src/diagrams/info/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

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
