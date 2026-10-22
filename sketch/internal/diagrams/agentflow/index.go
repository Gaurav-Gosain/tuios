// Ported from pele src/diagrams/agentflow/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

// Package agentflow draws agentflow diagrams: the steps, tools, decisions and documents of
// AI agents, in flows that nest, joined by sequence, reference and failure edges.
package agentflow

import "tuios.dev/sketch/internal/types"

// Agentflow is the agentflow diagram type.
var Agentflow = &types.Diagram{
	Type:         "agentflow",
	KeepComments: true,
	Parse: func(source string, config types.Config, title *string, lineOffset int, limits types.ParseLimits) any {
		// Mermaid reads this setting from the flowchart section for agentflow too.
		flow, _ := config["flowchart"].(map[string]any)
		inherit, _ := flow["inheritDir"].(bool)
		direction, _ := config["direction"].(string)
		db := NewAgentflowDb(AgentflowDbOptions{InheritDir: inherit, Direction: direction, LineOffset: lineOffset, MaxEdges: limits.MaxEdges})
		if title != nil && *title != "" {
			db.Title = *title
		}
		parseAgentflow(source, db)
		agentGraph(db, config)
		return db
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderAgentflow(model.(*AgentflowDb), config, options)
	},
}
