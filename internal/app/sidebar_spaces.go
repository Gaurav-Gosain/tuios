package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

func (m *OS) sidebarNodeIndent(node sessiontree.Node) int {
	indent := m.sidebarRowIndent()
	if node.Space != "" && node.Kind != sessiontree.KindSpace {
		indent += sidebarGroupIndent
	}
	return indent
}

func (m *OS) sidebarSpaceRow(node sessiontree.Node, cw int, pal overlay.Palette) string {
	indent := m.sidebarNodeIndent(node)
	name := sidebarStyle(nil, pal.FgDim).
		Render(overlay.Truncate(printableTitle(node.Title), sidebarNameAvailIn(cw, 0, indent)))
	gutter := sidebarGutter(false, "", nil, pal, &m.Settings)
	glyph := sidebarStyle(nil, nil).Render(" ")
	return sidebarComposeRuledRow(indent, gutter, glyph, name, "", cw, nil, pal, &m.Settings)
}
