package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The rail's space headings.
//
// A space is a name a person files related sessions under (tuios new --space,
// set-session-space). The sessions section draws a heading per space over its
// sessions, in the order and grouping tuios ls prints, and steps the sessions in
// under it. The sessions in no space come after every space at the section's
// own indent, with no heading. A rail with no session in any space draws exactly
// the rows it always did. See sessiontree.GroupBySpace.

// sidebarNodeIndent is the step a sessions-section row takes: the section's own
// step, and one group step more for a row drawn under a space's heading.
func (m *OS) sidebarNodeIndent(node sessiontree.Node) int {
	indent := m.sidebarRowIndent()
	if node.Space != "" && node.Kind != sessiontree.KindSpace {
		indent += sidebarGroupIndent
	}
	return indent
}

// sidebarSpaceRow renders one space's heading.
//
//	   payments ─────────────
//	^^ ^
//	|| the name, where a session's name would be at this level
//	|the glyph cell, blank: a heading has no state of its own
//	gutter
//
// It is drawn like a machine's heading, one level in: secondary ink, no bold,
// and the rule out to the spine, which is what makes it read as a heading with
// colour off. It is never lit, because nothing happens when you point at it.
func (m *OS) sidebarSpaceRow(node sessiontree.Node, cw int, pal overlay.Palette) string {
	indent := m.sidebarNodeIndent(node)
	name := sidebarStyle(nil, pal.FgDim).
		Render(overlay.Truncate(printableTitle(node.Title), sidebarNameAvailIn(cw, 0, indent)))
	gutter := sidebarGutter(false, "", nil, pal, &m.Settings)
	glyph := sidebarStyle(nil, nil).Render(" ")
	return sidebarComposeRuledRow(indent, gutter, glyph, name, "", cw, nil, pal, &m.Settings)
}
