//go:build !race

package sketch

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func perfSources(tb testing.TB) []string {
	tb.Helper()
	var srcs []string
	for _, c := range loadGoldens(tb) {
		if done[c.name] {
			srcs = append(srcs, c.source)
		}
	}
	return srcs
}

// BenchmarkRenderCorpus renders every golden source once an iteration. The
// ns/diagram figure is what docs/perf.md records for sketch.
func BenchmarkRenderCorpus(b *testing.B) {
	srcs := perfSources(b)
	o := RenderOptions{Now: goldenNow, Location: goldenZone}
	b.ReportAllocs()
	for b.Loop() {
		for _, src := range srcs {
			if _, err := Render(src, o); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(srcs)), "ns/diagram")
}

const renderAllocBudget = 260

// TestRenderAllocBudget holds the allocations of a render to a budget. An
// allocation count does not depend on the machine or its load, so it guards
// the warm render time without a clock.
func TestRenderAllocBudget(t *testing.T) {
	srcs := perfSources(t)
	o := RenderOptions{Now: goldenNow, Location: goldenZone}
	allocs := testing.AllocsPerRun(3, func() {
		for _, src := range srcs {
			Render(src, o)
		}
	}) / float64(len(srcs))
	t.Logf("%.0f allocations a render over %d sources", allocs, len(srcs))
	if allocs > renderAllocBudget {
		t.Errorf("a render makes %.0f allocations on average, over the budget of %d", allocs, renderAllocBudget)
	}
}

const initAllocBudget = 400

// TestInitBudget runs the test binary again with GODEBUG=inittrace=1 and
// holds the allocations of the init of sketch's internal packages to a
// budget. The root package is left out: in a test binary its init also
// builds the tests' own tables. The engine is left out with it, since it
// holds what the root package held before tuios.dev/sketch/core split off.
func TestInitBudget(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	allocs, records := 0, 0
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) < 11 || f[0] != "init" || !strings.HasPrefix(f[1], "tuios.dev/sketch/") || f[1] == "tuios.dev/sketch/internal/engine" || f[10] != "allocs" {
			continue
		}
		n, err := strconv.Atoi(f[9])
		if err != nil {
			t.Fatalf("cannot read %q", line)
		}
		allocs += n
		records++
	}
	if records == 0 {
		t.Fatalf("no init record of an internal package in the inittrace output; its format may have changed:\n%s", out)
	}
	t.Logf("sketch's internal packages make %d allocations at init", allocs)
	if allocs > initAllocBudget {
		t.Errorf("sketch's internal packages make %d allocations at init, over the budget of %d", allocs, initAllocBudget)
	}
}
