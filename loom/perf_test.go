package loom

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tuios.dev/tml"
)

// bigTable is a 200-row table under a stats row, the shape of a results
// surface, with a format on one column.
func bigTable() string {
	var rows []string
	for i := range 200 {
		rows = append(rows, fmt.Sprintf(`{"pkg": "internal/pkg%03d", "test": "TestCase%04d", "ms": %d, "line": "assertion %d failed at line %d"}`, i%37, i, (i*7919)%5000, i, i*3))
	}
	return `<tml version="1"><state>{"passed": 412, "failed": 200, "rows": [` + strings.Join(rows, ",") + `]}</state>
<col gap="1">
  <row gap="3"><stat label="passed" value="{state.passed}" role="success"/><stat label="failed" value="{state.failed}" role="error"/><spacer/><gauge value="{pct(state.passed, state.passed + state.failed)}" w="20"/></row>
  <table rows="state.rows" key="test" sort="ms:desc" select="single">
    <column field="pkg" title="Package" w="20"/>
    <column field="test" title="Test" w="fill"/>
    <column field="ms" title="Time" align="right" format="dur(value, 'ms')"/>
    <column field="line" title="First error line" w="fill:2"/>
  </table>
</col></tml>`
}

func BenchmarkRenderTable200(b *testing.B) {
	s := mustSurface(b, bigTable())
	b.ReportAllocs()
	for b.Loop() {
		s.Invalidate()
		s.Render(200, 55)
	}
}

// BenchmarkPatchOneBinding times the redraw after a patch to one binding.
// The patch itself (tml.Store.Apply, which copies the state) runs with the
// timer stopped; BenchmarkStoreApply times it.
func BenchmarkPatchOneBinding(b *testing.B) {
	s := mustSurface(b, bigTable())
	s.Render(200, 55)
	ops := [2][]tml.Op{
		{{Op: "replace", Path: "/passed", Value: []byte("413")}},
		{{Op: "replace", Path: "/passed", Value: []byte("412")}},
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		b.StopTimer()
		if err := s.Patch(ops[i%2]); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		s.Render(200, 55)
		i++
	}
}

// BenchmarkStoreApply times tml's side of the same patch, for comparison.
func BenchmarkStoreApply(b *testing.B) {
	s := mustSurface(b, bigTable())
	ops := []tml.Op{{Op: "replace", Path: "/passed", Value: []byte("413")}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.store.Apply(ops); err != nil {
			b.Fatal(err)
		}
	}
}

// TestRenderBudget holds a full render of a 200-row table at 200x55 to a
// time budget, and a patch to one binding (a stat above the table) to a
// smaller one. The work counters hold the same claim without the clock:
// the patch evaluates the two boxes that read the path and writes only the
// stats row, never the table.
func TestRenderBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timed")
	}
	full := testing.Benchmark(BenchmarkRenderTable200)
	patch := testing.Benchmark(BenchmarkPatchOneBinding)
	t.Logf("full render, 200 rows at 200x55: %d µs/op, %d B/op, %d allocs/op", full.NsPerOp()/1000, full.AllocedBytesPerOp(), full.AllocsPerOp())
	t.Logf("redraw after a one-binding patch: %d µs/op, %d B/op, %d allocs/op", patch.NsPerOp()/1000, patch.AllocedBytesPerOp(), patch.AllocsPerOp())
	apply := testing.Benchmark(BenchmarkStoreApply)
	t.Logf("the patch in tml.Store.Apply:     %d µs/op, %d B/op (not loom's; it copies the 200-row state)", apply.NsPerOp()/1000, apply.AllocedBytesPerOp())
	const fullBudget, patchBudget = 4_000_000, 100_000 // ns, about 7 times what a desktop measures
	if ns := full.NsPerOp(); ns > fullBudget {
		t.Errorf("a full render took %d µs, the budget is %d µs", ns/1000, fullBudget/1000)
	}
	if ns := patch.NsPerOp(); ns > patchBudget {
		t.Errorf("a one-binding patch took %d µs, the budget is %d µs", ns/1000, patchBudget/1000)
	}

	s := mustSurface(t, bigTable())
	s.Render(200, 55)
	whole := s.stats
	if err := s.Patch([]tml.Op{{Op: "replace", Path: "/passed", Value: []byte("413")}}); err != nil {
		t.Fatal(err)
	}
	s.Render(200, 55)
	t.Logf("full: %d boxes, %d cells; patch: %d boxes, %d cells", whole.built, whole.cells, s.stats.built, s.stats.cells)
	if s.stats.built > 2 {
		t.Errorf("the patch evaluated %d boxes, want 2 (the stat and the gauge)", s.stats.built)
	}
	if s.stats.cells > 200 {
		t.Errorf("the patch wrote %d cells, want at most the 200 of the stats row", s.stats.cells)
	}
}

// TestDeepRowChainBudget holds a chain of nested rows, as deep as tml.Check accepts, under 400 bytes
// and within tml's depth limit, to a render budget. Without the measure
// cache each row measured its child twice, so the chain cost 2^depth measures.
func TestDeepRowChainBudget(t *testing.T) {
	// The deepest chain tml.Check accepts.
	var s *Surface
	depth := 0
	for n := 32; n > 0 && s == nil; n-- {
		src := `<tml version="1">` + strings.Repeat("<row>", n) + `<text>x</text>` + strings.Repeat("</row>", n) + `</tml>`
		if doc, _, err := tml.Check([]byte(src), nil); err == nil {
			s, _ = New(doc, nil)
			depth = n
		}
	}
	if depth < 25 {
		t.Fatalf("tml accepts a row chain only %d deep", depth)
	}
	start := time.Now()
	s.Render(120, 40)
	took := time.Since(start)
	t.Logf("a %d-deep row chain: %d measures, %v", depth, s.stats.measures, took)
	if s.stats.measures > 200 {
		t.Errorf("the chain took %d measures, want at most 200", s.stats.measures)
	}
	if !testing.Short() && took > 50*time.Millisecond {
		t.Errorf("the chain took %v, the budget is 50 ms", took)
	}
}
