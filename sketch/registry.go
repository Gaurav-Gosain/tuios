// Ported from pele src/diagrams/registry.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// Only the types ported so far are listed.

package sketch

import (
	"tuios.dev/sketch/internal/diagrams/agentflow"
	"tuios.dev/sketch/internal/diagrams/architecture"
	"tuios.dev/sketch/internal/diagrams/block"
	"tuios.dev/sketch/internal/diagrams/c4"
	"tuios.dev/sketch/internal/diagrams/class"
	"tuios.dev/sketch/internal/diagrams/cynefin"
	"tuios.dev/sketch/internal/diagrams/er"
	"tuios.dev/sketch/internal/diagrams/eventmodeling"
	"tuios.dev/sketch/internal/diagrams/flowchart"
	"tuios.dev/sketch/internal/diagrams/gantt"
	"tuios.dev/sketch/internal/diagrams/git"
	"tuios.dev/sketch/internal/diagrams/info"
	"tuios.dev/sketch/internal/diagrams/ishikawa"
	"tuios.dev/sketch/internal/diagrams/journey"
	"tuios.dev/sketch/internal/diagrams/kanban"
	"tuios.dev/sketch/internal/diagrams/mindmap"
	"tuios.dev/sketch/internal/diagrams/packet"
	"tuios.dev/sketch/internal/diagrams/pie"
	"tuios.dev/sketch/internal/diagrams/quadrant"
	"tuios.dev/sketch/internal/diagrams/radar"
	"tuios.dev/sketch/internal/diagrams/railroad"
	"tuios.dev/sketch/internal/diagrams/requirement"
	"tuios.dev/sketch/internal/diagrams/sankey"
	"tuios.dev/sketch/internal/diagrams/sequence"
	"tuios.dev/sketch/internal/diagrams/state"
	"tuios.dev/sketch/internal/diagrams/swimlane"
	"tuios.dev/sketch/internal/diagrams/timeline"
	"tuios.dev/sketch/internal/diagrams/treemap"
	"tuios.dev/sketch/internal/diagrams/treeview"
	"tuios.dev/sketch/internal/diagrams/usecase"
	"tuios.dev/sketch/internal/diagrams/venn"
	"tuios.dev/sketch/internal/diagrams/wardley"
	"tuios.dev/sketch/internal/diagrams/xychart"
)

func init() {
	register(flowchart.Flowchart, sequence.Sequence, state.State, class.Class, er.Er, pie.Pie, mindmap.Mindmap, gantt.Gantt,
		xychart.XyChart, timeline.Timeline, quadrant.QuadrantChart, journey.Journey, kanban.Kanban, git.GitGraph, sankey.Sankey, block.BlockDiagram,
		architecture.Architecture, requirement.RequirementDiagram, c4.C4, packet.Packet, radar.Radar, treemap.Treemap,
		info.Info, treeview.TreeView, ishikawa.Ishikawa, cynefin.Cynefin, eventmodeling.EventModeling, venn.Venn, wardley.Wardley, railroad.Railroad, swimlane.Swimlane, usecase.Usecase, agentflow.Agentflow)
}
