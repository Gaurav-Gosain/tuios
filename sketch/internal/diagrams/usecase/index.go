// Ported from pele src/diagrams/usecase/index.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

// Package usecase draws use case diagrams: actors, use cases, notes and JSON tables,
// in system boundaries, joined by associations, includes, extends and generalizations.
package usecase

import "tuios.dev/sketch/internal/types"

// Usecase is the use case diagram type.
var Usecase = &types.Diagram{
	Type: "usecase",
	Math: true,
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return parseUsecase(source, title)
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderUsecase(model.(*UsecaseModel), config, options)
	},
}
