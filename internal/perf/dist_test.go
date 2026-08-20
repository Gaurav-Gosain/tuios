package perf

import (
	"strings"
	"testing"
	"time"
)

// TestQuantileNearestRank pins the quantiles to samples that were really taken.
// With 100 samples of 1ms..100ms the p50 must be a value from the set, not an
// average of two neighbours.
func TestQuantileNearestRank(t *testing.T) {
	var d Dist
	for i := 1; i <= 100; i++ {
		d.Add(time.Duration(i) * time.Millisecond)
	}
	s := d.Stats()
	for _, tc := range []struct {
		what string
		got  time.Duration
		want time.Duration
	}{
		{"min", s.Min, 1 * time.Millisecond},
		{"p50", s.P50, 50 * time.Millisecond},
		{"p95", s.P95, 95 * time.Millisecond},
		{"p99", s.P99, 99 * time.Millisecond},
		{"max", s.Max, 100 * time.Millisecond},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.what, tc.got, tc.want)
		}
	}
	if s.N != 100 {
		t.Errorf("N = %d, want 100", s.N)
	}
}

// TestQuantileSmallSamples checks the ends do not run off the slice, which is
// the failure mode that turns a harness into a panic mid-measurement.
func TestQuantileSmallSamples(t *testing.T) {
	var one Dist
	one.Add(7 * time.Millisecond)
	s := one.Stats()
	if s.Min != s.Max || s.P99 != 7*time.Millisecond {
		t.Errorf("single sample: %+v", s)
	}

	var empty Dist
	if got := (Dist(nil)).Quantile(0.5); got != 0 {
		t.Errorf("empty quantile = %v, want 0", got)
	}
	if empty.Stats().N != 0 {
		t.Error("empty stats should be zero")
	}
}

// TestStatsDoesNotReorder guards the caller's samples: a harness may want them
// in arrival order after reporting, to correlate a spike with what caused it.
func TestStatsDoesNotReorder(t *testing.T) {
	d := Dist{3 * time.Millisecond, 1 * time.Millisecond, 2 * time.Millisecond}
	_ = d.Stats()
	if d[0] != 3*time.Millisecond {
		t.Errorf("samples were reordered: %v", d)
	}
}

// TestLineIsOneRow keeps the report format greppable: every latency number in
// this project is one PERF line.
func TestLineIsOneRow(t *testing.T) {
	d := Dist{time.Millisecond}
	line := d.Line("echo/1 pane")
	if strings.Contains(line, "\n") {
		t.Errorf("Line spans rows: %q", line)
	}
	for _, want := range []string{"PERF", "echo/1 pane", "p50", "p99"} {
		if !strings.Contains(line, want) {
			t.Errorf("Line missing %q: %q", want, line)
		}
	}
}
