// Ported from pele src/diagrams/eventmodeling/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

// Package eventmodeling draws Mermaid's event modeling diagram: the frames of a system's
// timeline as boxes in swimlanes, with the relations between them, notes and specifications.
package eventmodeling

import "tuios.dev/sketch/internal/types"

// EventModeling is the eventmodeling diagram type.
var EventModeling = &types.Diagram{
	Type:         "eventmodeling",
	KeepComments: true,
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildEventModel(parseEventModel(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderEventModel(model.(*EventModelingModel), config, options)
	},
}
