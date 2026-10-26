// Ported from pele src/diagrams/journey/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

// Package journey draws Mermaid's user journey: tasks in order, each scored from 1 to 5
// and done by some actors, grouped in sections.
package journey

import "tuios.dev/sketch/internal/types"

// Journey is the user journey diagram type.
var Journey = &types.Diagram{
	Type: "journey",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := &JourneyDb{}
		db.title = types.Or(title, "")
		parseJourney(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderJourney(model.(*JourneyDb), config, options)
	},
}
