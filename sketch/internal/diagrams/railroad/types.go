// Ported from pele src/diagrams/railroad/types.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package railroad

import "math"

// RailroadNode is a node of a rule: terminal, nonterminal, special, sequence, choice, optional
// or repetition.
type RailroadNode struct {
	typ          string
	value        string // terminal
	name         string // nonterminal
	text         string // special
	elements     []*RailroadNode
	alternatives []*RailroadNode
	element      *RailroadNode
	// max is +Inf when the count has no upper bound.
	min, max float64
}

type RailroadRule struct {
	name       string
	definition *RailroadNode
}

// Notation is which of Mermaid's four grammars the text was written in.
type Notation = string

var inf = math.Inf(1)
