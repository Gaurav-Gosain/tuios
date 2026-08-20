// Package perf holds the shared measurement vocabulary for tuios's latency
// work: a sample set and the quantiles worth reading off it.
//
// It is a non-test package so both modules can use it. The e2e module sits
// under this one's import path, so it can reach in here for the same Dist the
// in-process harnesses report, and the two sets of numbers stay comparable.
package perf

import (
	"fmt"
	"sort"
	"time"
)

// Dist is a set of latency samples. Latency is felt at the tail, so nothing
// here reports a mean: a mean of a bimodal distribution names a value that
// never actually occurs.
type Dist []time.Duration

// Add appends a sample.
func (d *Dist) Add(v time.Duration) { *d = append(*d, v) }

// AddSince appends the time elapsed since t0.
func (d *Dist) AddSince(t0 time.Time) { *d = append(*d, time.Since(t0)) }

// sorted returns a sorted copy, leaving the caller's sample order alone.
func (d Dist) sorted() Dist {
	s := append(Dist(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s
}

// Quantile returns the q-th quantile by nearest rank. Nearest rank rather than
// interpolation because an interpolated p99 invents a duration that no
// keystroke ever took, and the question here is which real keystroke was slow.
func (d Dist) Quantile(q float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	return d.sorted().quantileOfSorted(q)
}

func (d Dist) quantileOfSorted(q float64) time.Duration {
	if q <= 0 {
		return d[0]
	}
	if q >= 1 {
		return d[len(d)-1]
	}
	i := int(q*float64(len(d)) + 0.5)
	if i > 0 {
		i--
	}
	if i >= len(d) {
		i = len(d) - 1
	}
	return d[i]
}

// Stats is a Dist reduced to the numbers a report quotes.
type Stats struct {
	N             int
	Min           time.Duration
	P50, P95, P99 time.Duration
	Max           time.Duration
}

// Stats reduces the samples once, so a report does not re-sort per quantile.
func (d Dist) Stats() Stats {
	if len(d) == 0 {
		return Stats{}
	}
	s := d.sorted()
	return Stats{
		N:   len(s),
		Min: s[0],
		P50: s.quantileOfSorted(0.50),
		P95: s.quantileOfSorted(0.95),
		P99: s.quantileOfSorted(0.99),
		Max: s[len(s)-1],
	}
}

// String renders one aligned line, the form every latency number in this
// project is quoted in.
func (s Stats) String() string {
	return fmt.Sprintf("n=%4d  min %9s  p50 %9s  p95 %9s  p99 %9s  max %9s",
		s.N, round(s.Min), round(s.P50), round(s.P95), round(s.P99), round(s.Max))
}

// Line labels a Stats for a log.
func (d Dist) Line(what string) string {
	return fmt.Sprintf("PERF %-38s %s", what, d.Stats())
}

// round trims a duration to a resolution worth printing. Sub-microsecond
// digits on a wall-clock measurement that crossed a socket are noise dressed
// as precision.
func round(v time.Duration) time.Duration {
	switch {
	case v < time.Microsecond:
		return v
	case v < time.Millisecond:
		return v.Round(100 * time.Nanosecond)
	default:
		return v.Round(10 * time.Microsecond)
	}
}
