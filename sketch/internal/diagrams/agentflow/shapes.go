// Ported from pele src/diagrams/agentflow/shapes.ts at 8aff183be57021b533f319a29341a529be934b08.

package agentflow

import "tuios.dev/sketch/internal/diagrams/flowchart"

// SHAPE_ALIASES are author-facing names for the shapes that carry meaning in an agentflow.
var SHAPE_ALIASES = map[string]string{
	"task":     "roundedRect",
	"tool":     "subroutine",
	"input":    "lean-right",
	"decision": "diamond",
	"refdoc":   "lin-doc",
	"action":   "hexagon",
	"round":    "rect",
}

var REMOVED_SHAPES = map[string]bool{
	"doc": true, "stadium": true, "terminal": true, "circle": true, "trapezoid": true, "inv_trapezoid": true, "inv-trapezoid": true,
	"doublecircle": true, "double-circle": true, "typeDeclaration": true, "procs": true, "lean_left": true, "lean-left": true,
	"in-out": true, "cylinder": true, "ellipse": true, "odd": true, "tag-rect": true, "tagged-rectangle": true, "delay": true,
	"half-rounded-rectangle": true, "lin-rect": true, "lined-rectangle": true, "win-pane": true, "window-pane": true,
	"curv-trap": true, "curved-trapezoid": true,
}

var ALLOWED_SHAPES = map[string]bool{
	"roundedRect": true, "subroutine": true, "subprocess": true, "subproc": true, "framed-rectangle": true, "lean-right": true,
	"diamond": true, "lin-doc": true, "lined-document": true, "hexagon": true, "hex": true, "connector": true, "collapsedGroup": true,
}

const DEFAULT_SHAPE = "roundedRect"

func resolveShapeAlias(shape string) string {
	if shape == "" {
		return shape
	}
	if alias, ok := SHAPE_ALIASES[shape]; ok {
		return alias
	}
	return shape
}

// isValidShape: Mermaid looks a shape name up with `in` on a plain object, so `constructor` passes there too.
func isValidShape(name string) bool {
	return flowchart.IsValidShape(name) || name == "constructor"
}

// normaliseNodeShapes reduces every node's shape to one agentflow draws, reporting the removed and unsupported ones.
func normaliseNodeShapes(nodes []*GraphNode, db *AgentflowDb) {
	for _, node := range nodes {
		if node.IsGroup {
			continue
		}
		if node.Shape == "" || node.Shape == "squareRect" || node.Shape == "rect" {
			node.Shape = DEFAULT_SHAPE
		}
		if REMOVED_SHAPES[node.Shape] {
			db.emitError("SHAPE_REMOVED", `shape "`+node.Shape+`" was removed in v0.8.1, using "`+DEFAULT_SHAPE+`"`, node.ID)
			node.Shape = DEFAULT_SHAPE
		} else if !ALLOWED_SHAPES[node.Shape] {
			db.emitWarning("SHAPE_UNSUPPORTED", `shape "`+node.Shape+`" is not supported, using "`+DEFAULT_SHAPE+`"`, node.ID)
			node.Shape = DEFAULT_SHAPE
		}
	}
}
