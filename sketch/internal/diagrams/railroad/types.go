package railroad

import "math"

// RailroadNode is a node of a rule: terminal, nonterminal, special, sequence, choice, optional
// or repetition.
type RailroadNode struct {
	typ          string
	value        string
	name         string
	text         string
	elements     []*RailroadNode
	alternatives []*RailroadNode
	element      *RailroadNode
	min, max     float64
}

type RailroadRule struct {
	name       string
	definition *RailroadNode
}

// Notation is which of Mermaid's four grammars the text was written in.
type Notation = string

var inf = math.Inf(1)
