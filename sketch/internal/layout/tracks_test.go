package layout

import (
	"testing"
	"time"
)

// TestPlainTracksStayLinear gives tracks to the pieces of one gap, too many to weigh, at two
// sizes sixteen times apart. Not part of pele. Trying every track against every piece on it took
// time quadratic in the pieces: stepped edges between two groups, 20,000 of them, took 3 s.
// The bound is on the ratio of the two times, which does not depend on the speed of the
// machine: under 40 for plainTracks, over 100 for the quadratic loop.
func TestPlainTracksStayLinear(t *testing.T) {
	shapes := map[string]func(n int) (from, to []float64){
		// Every piece crosses most others, and each needs a track of its own: n/2 tracks.
		"crossing": func(n int) (from, to []float64) {
			for i := range n {
				from = append(from, float64(20*i))
				to = append(to, float64(20*((i*7919)%n)+10))
			}
			return from, to
		},
		// Every piece leaves from one point, so all share one track.
		"fan": func(n int) (from, to []float64) {
			for i := range n {
				from = append(from, 0)
				to = append(to, float64(20*(i/2+1)*(1-2*(i%2))))
			}
			return from, to
		},
		// Fans of eight pieces that cross each other.
		"fans": func(n int) (from, to []float64) {
			for i := range n {
				from = append(from, float64(160*(i/8)))
				to = append(to, float64(20*((i*7919)%n)+10))
			}
			return from, to
		},
	}
	took := func(shape func(int) ([]float64, []float64), n int) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			from, to := shape(n)
			pieces := &Pieces{from: from, to: to, rank: make([]int, n)}
			start := time.Now()
			assignTracks(pieces, make([]int, 1), false, []float64{48}, make([]float64, 1))
			best = min(best, time.Since(start))
		}
		return best
	}
	for name, shape := range shapes {
		small, large := took(shape, 1000), took(shape, 16000)
		t.Logf("%s: %v for 1,000 pieces, %v for 16,000", name, small, large)
		if large > 80*small {
			t.Errorf("%s: 16,000 pieces took %v, %.0f times as long as 1,000, want at most 80", name, large, float64(large)/float64(small))
		}
	}
}
