package sortx

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"testing"
)

// item carries a key with many ties and its starting position, so two sorts
// that agree on keys but not on how they break ties show up as different.
type item struct {
	key int
	pos int
}

func items(n int, seed uint64) []item {
	r := rand.New(rand.NewPCG(seed, 1))
	out := make([]item, n)
	for i := range out {
		// Half as many keys as items: most keys repeat.
		out[i] = item{key: r.IntN(n/2 + 1), pos: i}
	}
	return out
}

func byKey(a, b item) int { return cmp.Compare(a.key, b.key) }

// TestSameOrderAsSlices is the claim the cold paths rely on: switching a call
// from slices to sortx changes no output, not even the order of ties in the
// unstable sort. The sizes cross the insertion-sort and ninther thresholds of
// pdqsort and the block size of the stable sort.
func TestSameOrderAsSlices(t *testing.T) {
	for seed := range uint64(500) {
		for _, n := range []int{0, 1, 2, 3, 12, 13, 20, 40, 51, 200, 1500} {
			want, got := items(n, seed), items(n, seed)
			slices.SortFunc(want, byKey)
			SortFunc(got, byKey)
			if !slices.Equal(want, got) {
				t.Fatalf("SortFunc: seed %d, n %d: order differs from slices.SortFunc", seed, n)
			}

			want, got = items(n, seed), items(n, seed)
			slices.SortStableFunc(want, byKey)
			SortStableFunc(got, byKey)
			if !slices.Equal(want, got) {
				t.Fatalf("SortStableFunc: seed %d, n %d: order differs from slices.SortStableFunc", seed, n)
			}

			want, got = items(n, seed), items(n, seed)
			slices.SortStableFunc(want, byKey)
			ShortStableFunc(got, byKey)
			if !slices.Equal(want, got) {
				t.Fatalf("ShortStableFunc: seed %d, n %d: order differs from slices.SortStableFunc", seed, n)
			}
		}
	}
}
