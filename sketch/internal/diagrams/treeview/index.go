// Ported from pele src/diagrams/treeview/index.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package treeview draws Mermaid's treeView diagram: a file tree, as an indented list with
// guide lines, icons and descriptions.
package treeview

import "tuios.dev/sketch/internal/types"

// TreeView is the treeView diagram type.
var TreeView = &types.Diagram{
	Type: "treeView",
	Parse: func(source string, _ types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildTreeView(readTreeView(source), types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderTreeView(model.(*TreeViewModel), config, options)
	},
}
