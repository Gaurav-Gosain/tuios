package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The rail's space groups.
//
// A space is a name a person files related sessions under (tuios new --space,
// set-session-space). The rail draws it as a header over its sessions, with
// the same row a repository's group gets, and steps the sessions in under it.
// A space is the outer group: the repository grouping runs inside each space
// and inside the sessions in no space, which come after every space at the
// section's own indent. A rail with no session in any space draws exactly the
// rows it always did. See sessiontree.GroupBySpace.

// sidebarNodeIndent is the step a sessions-section row takes: the section's
// own step, and one group step more for a row inside a space.
func (m *OS) sidebarNodeIndent(node sessiontree.Node) int {
	indent := m.sidebarRowIndent()
	if node.Space != "" && node.Kind != sessiontree.KindSpace && node.Host == "" {
		indent += sidebarGroupIndent
	}
	return indent
}

// sidebarSpaceRow renders one space's group header.
func (m *OS) sidebarSpaceRow(node sessiontree.Node, cw int, pal overlay.Palette, st sidebarRowState) string {
	return m.sidebarGroupHeaderRow(node, m.SidebarSpaceCollapsed(node.ID), true, cw, pal, st)
}

// SidebarSpaceCollapsed reports whether a space's group is folded shut.
func (m *OS) SidebarSpaceCollapsed(space string) bool {
	return m.SidebarCollapsedSpaces[space]
}

// SidebarToggleSpaceCollapsed folds a space's group shut, or opens it again,
// and remembers which it is, keyed by space name the way repositories are.
func (m *OS) SidebarToggleSpaceCollapsed(space string) {
	if space == "" {
		return
	}
	if m.SidebarCollapsedSpaces[space] {
		delete(m.SidebarCollapsedSpaces, space)
	} else {
		if m.SidebarCollapsedSpaces == nil {
			m.SidebarCollapsedSpaces = make(map[string]bool, 1)
		}
		m.SidebarCollapsedSpaces[space] = true
	}
	m.saveSidebarState()
}
