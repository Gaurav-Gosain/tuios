// Ported from pele src/diagrams/railroad/types.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
