// Ported from pele src/diagrams/registry.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.
//
// Only the types ported so far are listed.

package sketch

import (
	"tuios.dev/sketch/internal/diagrams/class"
	"tuios.dev/sketch/internal/diagrams/er"
	"tuios.dev/sketch/internal/diagrams/flowchart"
	"tuios.dev/sketch/internal/diagrams/gantt"
	"tuios.dev/sketch/internal/diagrams/journey"
	"tuios.dev/sketch/internal/diagrams/mindmap"
	"tuios.dev/sketch/internal/diagrams/pie"
	"tuios.dev/sketch/internal/diagrams/quadrant"
	"tuios.dev/sketch/internal/diagrams/sequence"
	"tuios.dev/sketch/internal/diagrams/state"
	"tuios.dev/sketch/internal/diagrams/timeline"
	"tuios.dev/sketch/internal/diagrams/xychart"
)

func init() {
	register(flowchart.Flowchart, sequence.Sequence, state.State, class.Class, er.Er, pie.Pie, mindmap.Mindmap, gantt.Gantt,
		xychart.XyChart, timeline.Timeline, quadrant.QuadrantChart, journey.Journey)
}
