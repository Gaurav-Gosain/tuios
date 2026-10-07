package app

import (
	"fmt"
	"testing"
)

// crowdedNamedDockOS is a 120-column bar with four named workspaces and n
// minimized panes whose names do not fit beside them, so every frame has to
// shorten the names.
func crowdedNamedDockOS(t testing.TB, n int) *OS {
	t.Helper()
	m := dockCrowdedOS(t, 120, 4, n)
	for i, w := range m.Windows {
		if w.Minimized {
			w.CustomName = fmt.Sprintf("a-long-pane-name-%02d-build", i)
		}
	}
	return m
}

// BenchmarkDockCrowded draws the dock with named minimized panes that do not
// fit at their full names. With twelve of them the names do not fit even at
// the shortest budget, which is the case that tried every budget.
func BenchmarkDockCrowded(b *testing.B) {
	for _, n := range []int{4, 12} {
		b.Run(fmt.Sprintf("min-%d", n), func(b *testing.B) {
			m := crowdedNamedDockOS(b, n)
			b.ReportAllocs()
			for b.Loop() {
				_, _ = m.renderDockString()
			}
		})
	}
}

// TestCrowdedDockAllocationBudget holds a crowded dock's frame to a budget.
// Searching the name budget one cell at a time made 7,678 allocations a
// frame with twelve named entries; bisecting it makes about 900.
func TestCrowdedDockAllocationBudget(t *testing.T) {
	m := crowdedNamedDockOS(t, 12)
	const budget = 2000
	if n := testing.AllocsPerRun(20, func() { _, _ = m.renderDockString() }); n > budget {
		t.Fatalf("a crowded dock frame made %.0f allocations, over the budget of %d", n, budget)
	}
}
