// Package rare lists the diagram types beyond the eight common ones.
package rare

import (
	"tuios.dev/sketch/internal/types"

	"tuios.dev/sketch/internal/diagrams/agentflow"
	"tuios.dev/sketch/internal/diagrams/architecture"
	"tuios.dev/sketch/internal/diagrams/block"
	"tuios.dev/sketch/internal/diagrams/c4"
	"tuios.dev/sketch/internal/diagrams/cynefin"
	"tuios.dev/sketch/internal/diagrams/eventmodeling"
	"tuios.dev/sketch/internal/diagrams/git"
	"tuios.dev/sketch/internal/diagrams/info"
	"tuios.dev/sketch/internal/diagrams/ishikawa"
	"tuios.dev/sketch/internal/diagrams/journey"
	"tuios.dev/sketch/internal/diagrams/kanban"
	"tuios.dev/sketch/internal/diagrams/packet"
	"tuios.dev/sketch/internal/diagrams/quadrant"
	"tuios.dev/sketch/internal/diagrams/radar"
	"tuios.dev/sketch/internal/diagrams/railroad"
	"tuios.dev/sketch/internal/diagrams/requirement"
	"tuios.dev/sketch/internal/diagrams/sankey"
	"tuios.dev/sketch/internal/diagrams/swimlane"
	"tuios.dev/sketch/internal/diagrams/timeline"
	"tuios.dev/sketch/internal/diagrams/treemap"
	"tuios.dev/sketch/internal/diagrams/treeview"
	"tuios.dev/sketch/internal/diagrams/usecase"
	"tuios.dev/sketch/internal/diagrams/venn"
	"tuios.dev/sketch/internal/diagrams/wardley"
	"tuios.dev/sketch/internal/diagrams/xychart"
)

// Diagrams are the types, in the order the registry lists them.
var Diagrams = []*types.Diagram{
	xychart.XyChart,
	timeline.Timeline,
	quadrant.QuadrantChart,
	journey.Journey,
	kanban.Kanban,
	git.GitGraph,
	sankey.Sankey,
	block.BlockDiagram,
	architecture.Architecture,
	requirement.RequirementDiagram,
	c4.C4,
	packet.Packet,
	radar.Radar,
	treemap.Treemap,
	info.Info,
	treeview.TreeView,
	ishikawa.Ishikawa,
	cynefin.Cynefin,
	eventmodeling.EventModeling,
	venn.Venn,
	wardley.Wardley,
	railroad.Railroad,
	swimlane.Swimlane,
	usecase.Usecase,
	agentflow.Agentflow,
}
