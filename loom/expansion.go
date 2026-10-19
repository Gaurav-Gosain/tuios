package loom

import "tuios.dev/tml"

// expansion is the budget a renderer spends as it expands each and list.
// tml checks the expansion when a document arrives and after every patch
// that goes through a Store made by StoreFor. A renderer that expands from
// another source of state spends this budget, so it stops at the same
// limit.
type expansion struct {
	left int
}

// newExpansion returns a budget of lim.Each expanded nodes. A nil lim means
// the default.
func newExpansion(lim *tml.Limits) *expansion {
	each := tml.DefaultLimits().Each
	if lim != nil && lim.Each > 0 {
		each = lim.Each
	}
	return &expansion{left: each}
}

// Take spends n nodes. It reports false, and spends nothing, when fewer
// than n are left. The renderer then stops expanding.
func (e *expansion) Take(n int) bool {
	if n < 0 || n > e.left {
		return false
	}
	e.left -= n
	return true
}

// Left returns the nodes still allowed.
func (e *expansion) Left() int { return e.left }
