// Ported from pele src/diagrams/venn/types.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package venn

// VennSubset is a set (one id) or an overlap of sets (two or more ids, sorted), with its relative area.
type VennSubset struct {
	sets     []string
	size     float64
	label    string
	hasLabel bool
}

// VennText is a line of text shown inside the region of the given sets.
type VennText struct {
	sets     []string
	id       string
	label    string
	hasLabel bool
}

// VennStyle: targets are the sorted ids of a set or overlap, or the id of a text. The styles keep
// their order.
type VennStyle struct {
	targets []string
	styles  [][2]string
}
