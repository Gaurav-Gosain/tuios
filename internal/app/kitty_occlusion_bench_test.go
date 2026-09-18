package app

import (
	"fmt"
	"testing"
)

// benchWindows builds n panes in a row, each drawn over the one before it, so
// every pane has a real occluder list to walk.
func benchWindows(n int) map[string]*WindowPositionInfo {
	all := make(map[string]*WindowPositionInfo, n)
	for i := range n {
		all[fmt.Sprintf("w%02d", i)] = &WindowPositionInfo{
			WindowX: i * 12, WindowY: 0, Width: 40, Height: 30,
			Visible: true, WindowZ: i,
			ScreenWidth: 400, ScreenHeight: 60,
		}
	}
	return all
}

// BenchmarkOccluderScan measures what a refresh pays per window to work out
// what is drawn over it. A drag runs this on every frame.
func BenchmarkOccluderScan(b *testing.B) {
	all := benchWindows(12)
	img := cellRect{X: 20, Y: 5, W: 30, H: 12}
	var scratch []cellRect

	b.ReportAllocs()
	for b.Loop() {
		scratch = occludersAboveInto(scratch[:0], 3, all, "w03")
		if _, ok := largestClearRect(img, scratch); !ok {
			b.Fatal("image fully covered, benchmark is not measuring the interesting path")
		}
	}
}

// BenchmarkOccluderScanAllocating is the same work done the way the first cut
// did it, building a fresh slice per call, for comparison.
func BenchmarkOccluderScanAllocating(b *testing.B) {
	all := benchWindows(12)
	img := cellRect{X: 20, Y: 5, W: 30, H: 12}

	b.ReportAllocs()
	for b.Loop() {
		blockers := occludersAbove(3, all, "w03")
		if _, ok := largestClearRect(img, blockers); !ok {
			b.Fatal("image fully covered, benchmark is not measuring the interesting path")
		}
	}
}
