package raster

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestNestedGroupLayersStayBounded draws 200 nested groups with an opacity,
// each with text, on a 4000 by 4000 image. Each group used to get a layer of
// its own, 64 MB a level, so this asked for about 12 GB. There is now one
// layer at a time, and its pixels count against the work budget.
func TestNestedGroupLayersStayBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="4000" height="4000">`)
	for range 200 {
		b.WriteString(`<g opacity="0.9"><text x="10" y="20">a</text><rect x="0" y="0" width="4000" height="4000" fill="red"/>`)
	}
	for range 200 {
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	if _, err := Draw(b.String(), Options{}); err != nil {
		t.Fatalf("draw: %v", err)
	}
	runtime.ReadMemStats(&after)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("draw took %v", d)
	}
	got := after.TotalAlloc - before.TotalAlloc
	t.Logf("allocated %d MB", got>>20)
	if got > 512<<20 {
		t.Fatalf("draw allocated %d MB, want under 512 MB", got>>20)
	}
}
