// Ported from pele src/diagrams/cynefin/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

// Package cynefin draws Mermaid's cynefin diagram: the four domains of the Cynefin framework
// around confusion in the middle, with the items placed in each and the transitions between them.
package cynefin

import "tuios.dev/sketch/internal/types"

// Cynefin is the cynefin diagram type.
var Cynefin = &types.Diagram{
	Type: "cynefin",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildCynefin(parseCynefin(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderCynefin(model.(*CynefinModel), config, options)
	},
}
