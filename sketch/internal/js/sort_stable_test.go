package js

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// stableItem carries a sort key and the element's starting index, so two sorts
// that agree on keys but break ties or read a comparator differently show up as
// two different orders of ids.
type stableItem struct {
	key float64
	id  int
}

// byKey is (a, b) => a.key - b.key. NaN keys and opposite infinities make it
// give NaN, which Sort counts as 0.
func byKey(a, b stableItem) float64 { return a.key - b.key }

// byPairHash is inconsistent but deterministic: its result is a hash of the
// ordered pair (a.id, b.id) mapped to -1, 0 or 1, so the result for (a, b) and
// the result for (b, a) are independent of each other. Two sorts give the same
// order under it only when they ask the same questions in the same sequence.
func byPairHash(a, b stableItem) float64 {
	x := uint64(uint32(a.id))<<32 | uint64(uint32(b.id))
	// splitmix64 finalizer.
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	x ^= x >> 31
	return float64(x%3) - 1
}

// sortByBase is Sort as it was with slices.SortStableFunc on the longer
// arrays, both branches, copied whole. Sort must keep giving its order.
func sortByBase[T any](a []T, cmp func(x, y T) float64) {
	order := func(x, y T) float64 {
		if v := cmp(x, y); !math.IsNaN(v) {
			return v
		}
		return 0
	}
	n := len(a)
	if n < 2 {
		return
	}
	if n >= 64 {
		slices.SortStableFunc(a, func(x, y T) int {
			switch v := order(x, y); {
			case v < 0:
				return -1
			case v > 0:
				return 1
			}
			return 0
		})
		return
	}
	// CountAndMakeRun: the run at the start, reversed when it is strictly descending.
	run := 2
	descending := order(a[1], a[0]) < 0
	previous := a[1]
	for i := 2; i < n; i++ {
		v := order(a[i], previous)
		if descending {
			if v >= 0 {
				break
			}
		} else if v < 0 {
			break
		}
		previous = a[i]
		run++
	}
	if descending {
		slices.Reverse(a[:run])
	}
	// BinaryInsertionSort of the rest into the run.
	for start := run; start < n; start++ {
		left, right := 0, start
		pivot := a[start]
		for left < right {
			mid := left + (right-left)>>1
			if order(pivot, a[mid]) < 0 {
				right = mid
			} else {
				left = mid + 1
			}
		}
		copy(a[left+1:start+1], a[left:start])
		a[left] = pivot
	}
}

// TestSortMatchesTheStableSortItReplaces sorts the same arrays with Sort and
// with sortByBase and requires the same order of ids: on random arrays of 2 to
// 500 elements, on fixed arrays at 63, 64 and 65 elements, around the length
// where Sort changes algorithm, and on fixed arrays that start strictly
// descending and then meet a NaN key, where a NaN result and a 0 result differ.
func TestSortMatchesTheStableSortItReplaces(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	// keys draws n keys from a small set, so most keys tie. Some draws add NaN,
	// the infinities or both.
	keys := func(r *rand.Rand, n int, withNaN, withInf bool) []float64 {
		set := []float64{-2, -1, 0, 0.5, 1, 3}
		if withNaN {
			set = append(set, nan)
		}
		if withInf {
			set = append(set, inf, -inf)
		}
		out := make([]float64, n)
		for i := range out {
			out[i] = set[r.IntN(len(set))]
		}
		return out
	}
	items := func(ks []float64) []stableItem {
		out := make([]stableItem, len(ks))
		for i, k := range ks {
			out[i] = stableItem{key: k, id: i}
		}
		return out
	}
	ids := func(a []stableItem) []int {
		out := make([]int, len(a))
		for i, it := range a {
			out[i] = it.id
		}
		return out
	}
	check := func(t *testing.T, name string, cmp func(a, b stableItem) float64, seed uint64, ks []float64) {
		t.Helper()
		want, got := items(ks), items(ks)
		sortByBase(want, cmp)
		Sort(got, cmp)
		if w, g := ids(want), ids(got); !slices.Equal(w, g) {
			t.Fatalf("seed %d, n %d, comparator %s: ids differ\nSort:       %v\nsortByBase: %v", seed, len(ks), name, g, w)
		}
	}

	comparators := []struct {
		name string
		cmp  func(a, b stableItem) float64
	}{
		{"byKey", byKey},
		{"byPairHash", byPairHash},
	}
	for _, c := range comparators {
		t.Run(c.name, func(t *testing.T) {
			t.Run("random", func(t *testing.T) {
				for seed := range uint64(3000) {
					r := rand.New(rand.NewPCG(seed, 0x50a7))
					var n int
					switch {
					case seed%10 == 0:
						n = 2 + r.IntN(62)
					case seed%25 == 1:
						n = 64
					default:
						n = 64 + r.IntN(437)
					}
					check(t, c.name, c.cmp, seed, keys(r, n, seed%5 < 2, seed%5 == 0 || seed%5 == 2))
				}
			})
			for _, n := range []int{63, 64, 65} {
				t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
					for i := range uint64(60) {
						seed := uint64(n)<<16 | i
						r := rand.New(rand.NewPCG(seed, 0xf1ed))
						check(t, c.name, c.cmp, seed, keys(r, n, i%2 == 0, i%3 == 0))
					}
				})
			}
		})
	}

	t.Run("byKey descending then NaN", func(t *testing.T) {
		for i := range uint64(60) {
			seed := 0xde5c<<16 | i
			r := rand.New(rand.NewPCG(seed, 0xa11))
			n := 3 + r.IntN(61)
			// A strictly descending run of at least two keys, a NaN inside
			// it, and more descending keys after it, then random keys.
			at := 2 + r.IntN(n-2)
			ks := make([]float64, n)
			for j := range ks {
				ks[j] = float64(1000 - 10*j)
			}
			ks[at] = nan
			rest := at + 1 + r.IntN(n-at)
			for j := rest; j < n; j++ {
				ks[j] = float64(r.IntN(2000) - 500)
			}
			check(t, "byKey", byKey, seed, ks)
		}
	})
}
