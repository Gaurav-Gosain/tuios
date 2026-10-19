// Ported from pele src/diagrams/registry.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// Only the types ported so far are listed.

package sketch

import (
	"tuios.dev/sketch/internal/diagrams/class"
	"tuios.dev/sketch/internal/diagrams/er"
	"tuios.dev/sketch/internal/diagrams/flowchart"
	"tuios.dev/sketch/internal/diagrams/gantt"
	"tuios.dev/sketch/internal/diagrams/mindmap"
	"tuios.dev/sketch/internal/diagrams/pie"
	"tuios.dev/sketch/internal/diagrams/sequence"
	"tuios.dev/sketch/internal/diagrams/state"
)

func init() {
	register(flowchart.Flowchart, sequence.Sequence, state.State, class.Class, er.Er, pie.Pie, mindmap.Mindmap, gantt.Gantt)
}
