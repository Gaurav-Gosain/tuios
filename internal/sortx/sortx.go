// Package sortx sorts a slice through one shared, non-generic copy of the
// sort code, to keep the binary small.
//
// Every call to slices.SortFunc or slices.SortStableFunc with a new element
// type stamps out its own copy of pdqsort or of the stable sort, several
// kilobytes each. sort.Slice and sort.SliceStable are compiled once and reach
// the elements through a reflect-built swapper.
//
// Use it on a cold path: a CLI command's output, a listing a verb returns,
// config and manifest load, a write to disk, a one-off prompt. Each call
// allocates three times (about 250 bytes) and runs up to twice as slow as the
// slices function, so keep slices.SortFunc and slices.SortStableFunc on a hot
// path: anything a render, a keystroke, an event burst or a broadcast runs.
//
// The order is the same as the slices function gives, ties included, because
// both packages run the same algorithm: pdqsort for SortFunc, insertion and
// symmerge for SortStableFunc. sortx_test.go checks that on random input.
package sortx

import "sort"

// SortFunc sorts s in ascending order as cmp decides, like slices.SortFunc.
// It is not stable.
func SortFunc[S ~[]E, E any](s S, cmp func(a, b E) int) {
	sort.Slice(s, func(i, j int) bool { return cmp(s[i], s[j]) < 0 })
}

// SortStableFunc sorts s like slices.SortStableFunc and keeps equal elements
// in their original order.
func SortStableFunc[S ~[]E, E any](s S, cmp func(a, b E) int) {
	sort.SliceStable(s, func(i, j int) bool { return cmp(s[i], s[j]) < 0 })
}
