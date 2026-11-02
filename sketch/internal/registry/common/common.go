// Package common lists the eight common diagram types, which tuios.dev/sketch/core draws.
package common

import (
	"tuios.dev/sketch/internal/types"

	"tuios.dev/sketch/internal/diagrams/class"
	"tuios.dev/sketch/internal/diagrams/er"
	"tuios.dev/sketch/internal/diagrams/flowchart"
	"tuios.dev/sketch/internal/diagrams/gantt"
	"tuios.dev/sketch/internal/diagrams/mindmap"
	"tuios.dev/sketch/internal/diagrams/pie"
	"tuios.dev/sketch/internal/diagrams/sequence"
	"tuios.dev/sketch/internal/diagrams/state"
)

// Diagrams are the types, in the order the registry lists them.
var Diagrams = []*types.Diagram{
	flowchart.Flowchart,
	sequence.Sequence,
	state.State,
	class.Class,
	er.Er,
	pie.Pie,
	mindmap.Mindmap,
	gantt.Gantt,
}
