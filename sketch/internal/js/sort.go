package js

import (
	"math"
	"slices"
)

// Sort is Array.prototype.sort with a comparator, as V8 runs it. A comparator
// that gives NaN counts as 0, as the specification says. When the comparator
// is consistent, any stable sort gives the same order. When it is not, as with
// a NaN that equals everything, the order depends on the algorithm: Sort
// follows V8's for arrays under 64 elements, where V8's TimSort is one run
// sorted by binary insertion. A longer array is sorted by slices.SortStableFunc.
func Sort[T any](a []T, cmp func(x, y T) float64) {
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
