// Ported from pele src/diagrams/kanban/index.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

// Package kanban draws Mermaid's kanban board: columns of cards, each card with an
// optional ticket, assignee, priority and icon.
package kanban

import "tuios.dev/sketch/internal/types"

// Kanban is the kanban diagram type.
var Kanban = &types.Diagram{
	Type: "kanban",
	Math: true,
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		db := &KanbanDb{}
		db.title = types.Or(title, "")
		parseKanban(source, db)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderKanban(model.(*KanbanDb), config, options)
	},
}
