package loom

import "tuios.dev/tml"

type expansion struct {
	left int
}

func newExpansion(lim *tml.Limits) *expansion {
	each := tml.DefaultLimits().Each
	if lim != nil && lim.Each > 0 {
		each = lim.Each
	}
	return &expansion{left: each}
}

func (e *expansion) Take(n int) bool {
	if n < 0 || n > e.left {
		return false
	}
	e.left -= n
	return true
}

func (e *expansion) Left() int { return e.left }
