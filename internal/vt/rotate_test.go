package vt

import (
	"slices"
	"testing"
)

// TestRotateLeftMatchesThreeReversals holds rotateLeft to the three-reversal
// rotation it replaced, for every length and split up to past both buffer
// cut-offs, so each of its three paths is covered.
func TestRotateLeftMatchesThreeReversals(t *testing.T) {
	for n := 0; n <= 40; n++ {
		for k := 0; k <= n; k++ {
			want := make([]int, n)
			for i := range want {
				want[i] = i
			}
			got := slices.Clone(want)
			if k > 0 && k < n {
				slices.Reverse(want[:k])
				slices.Reverse(want[k:])
				slices.Reverse(want)
			}
			rotateLeft(got, k)
			if !slices.Equal(got, want) {
				t.Fatalf("rotateLeft(n=%d, k=%d) = %v, want %v", n, k, got, want)
			}
		}
	}
}
