// Ported from pele src/diagrams/treeview/index.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

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
