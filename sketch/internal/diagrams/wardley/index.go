// Ported from pele src/diagrams/wardley/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

// Package wardley draws Mermaid's wardley map: components of a value chain placed by their
// visibility to the user and their evolution, with links, pipelines, trends, annotations and
// forces.
package wardley

import "tuios.dev/sketch/internal/types"

// Wardley is the wardley diagram type.
var Wardley = &types.Diagram{
	Type:    "wardley",
	Section: "wardley-beta",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildWardley(parseWardley(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderWardley(model.(*WardleyModel), config, options)
	},
}
