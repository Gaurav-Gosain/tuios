package main

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
)

// TestBigDiffScrollsWithinBudget is a perf budget: a checkpoint of one file
// with 4,900 changed lines, scrolled. The page built every line, three
// elements each, every frame: 34 to 48 ms a frame and 56 MB allocated per
// frame on an M-series Mac (Atlas, MYGO_FRAME_STATS, 2026-11-09), where
// 120 Hz allows 8.3 ms. It builds the lines in view now: 7.2 ms and 1.4 MB.
//
// The budget is the bytes a scrolled frame allocates, which does not depend
// on the machine's speed or its load. In the headless tester, which also
// draws each frame into a bitmap, a frame took 1,871 MB before the fix and
// 28 MB after; the budget is 128 MB.
func TestBigDiffScrollsWithinBudget(t *testing.T) {
	const lines = 2450
	f := ckFile{Path: "mid.go", Status: "M", Added: lines, Removed: lines}
	h := ckHunk{OldStart: 1, NewStart: 1}
	for i := 1; i <= lines; i++ {
		h.Lines = append(h.Lines, hunkLine{Op: "-", Text: fmt.Sprintf("func g%d(x int) int { return x*%d + len(\"line %d\") } // original %d", i, i, i, i)})
	}
	for i := 1; i <= lines; i++ {
		h.Lines = append(h.Lines, hunkLine{Op: "+", Text: fmt.Sprintf("func g%d(x int) int { return (x+3)*%d + len(\"line %d\") } // edited %d", i, i, i, i)})
	}
	f.Hunks = []ckHunk{h}
	d := &ckDiff{files: []ckFile{f}}
	a := &App{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.diffPage(c, paletteFor(c), client.Agent{}, d, false, nil)
		})
	}, 900, 700)
	for range 4 {
		tester.Frame()
	}
	var before, after runtime.MemStats
	const frames = 20
	runtime.ReadMemStats(&before)
	for range frames {
		tester.Scroll(450, 400, 0, 60)
		tester.Frame()
	}
	runtime.ReadMemStats(&after)
	per := float64(after.TotalAlloc-before.TotalAlloc) / frames
	t.Logf("%.0f KB allocated per scrolled frame", per/1024)
	if d.scroll.Y == 0 {
		t.Fatalf("the page never scrolled, so the budget proves nothing")
	}
	if per > 128<<20 {
		t.Fatalf("ASSERTION: a scrolled frame of a 4,900-line diff allocates %.1f MB, over the 128 MB budget", per/(1<<20))
	}
}

// hunkLine is a line of a ckHunk.
type hunkLine = struct {
	Op   string `json:"op"`
	Old  int    `json:"old"`
	New  int    `json:"new"`
	Text string `json:"text"`
}
