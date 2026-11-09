package app

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
)

// TestQuickLookCutLineBytes holds a quick look step with wrap off to a budget
// of bytes allocated per frame. Each drawn row handed the whole line to the
// code drawer, which styled it and walked it to the end to cut one row of
// cells, so a step through 64 KiB lines allocated megabytes and took tens of
// milliseconds. Bytes are a count, steady on a loaded machine where a clock
// is not. A small file must cost no more than it did.
func TestQuickLookCutLineBytes(t *testing.T) {
	// Under -race sync.Pool drops a share of what is put back, so the
	// count moves from run to run.
	skipAllocCountsUnderRace(t)
	m := keystrokeOS(t, 1, 200, 60)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())

	// perFrame opens the text with wrap off, steps one line down with j, and
	// returns the median of the bytes each of 21 frames of that view
	// allocates, drawn again from nothing each time: the panel keeps a frame
	// where nothing changed, and every step changes the scroll. The bytes of
	// one frame move in steps of a few KiB with what else the process does;
	// the median does not follow one such step.
	perFrame := func(t *testing.T, name string, lines []string, spans [][]diffview.Span) uint64 {
		t.Helper()
		target := quickLookTarget{path: "/src/" + name, name: name}
		m.quickLook = quickLookState{open: true, target: target, res: &quickLookResult{
			target: target, kind: "text", size: int64(len(strings.Join(lines, "\n"))), lines: lines, spans: spans,
		}}
		_, _, _ = m.renderQuickLook()
		if !m.QuickLookLine(true) || m.quickLook.scroll != 1 {
			t.Fatalf("j did not move the text one line down: scroll %d", m.quickLook.scroll)
		}
		m.quickLook.framed = quickLookFrame{}
		_, _, _ = m.renderQuickLook()
		got := make([]uint64, 21)
		var before, after runtime.MemStats
		for i := range got {
			m.quickLook.framed = quickLookFrame{}
			runtime.ReadMemStats(&before)
			_, _, _ = m.renderQuickLook()
			runtime.ReadMemStats(&after)
			got[i] = after.TotalAlloc - before.TotalAlloc
		}
		slices.Sort(got)
		return got[len(got)/2]
	}

	// Measured at d95718d05 with go1.27.3 on 2026-11-09: a median of 3,901
	// KiB a frame, every drawn row a different long line. A row drawn from
	// the part of the line it can show stays far under the budget; a cut
	// line drawn from the whole line again is well over it.
	t.Run("wrap off, 60 lines of 64 KiB", func(t *testing.T) {
		const budget = 3 << 19
		long := strings.Repeat("x", 64<<10)
		lines := make([]string, 60)
		for i := range lines {
			lines[i] = long
		}
		if n := perFrame(t, "data.log", lines, nil); n > budget {
			t.Errorf("a step through 60 cut lines of 64 KiB allocated %d KiB a frame, want at most %d KiB", n>>10, budget>>10)
		}
	})

	// Every line here is at most 300 bytes, so it is drawn whole as before.
	// This guards what a frame adds around the rows, such as a buffer made
	// for every frame or row. Measured at d95718d05 with go1.27.3 on
	// 2026-11-09: a median of 811 KiB a frame in each of 8 runs of one
	// process and 3 fresh processes, held to that plus 16 KiB.
	t.Run("small file", func(t *testing.T) {
		const budget = (811 + 16) << 10
		lines := make([]string, 2000)
		for i := range lines {
			lines[i] = fmt.Sprintf("var v%04d = %q // line %d", i+1, "row", i+1)
		}
		for i, n := range []int{129, 170, 210, 255, 300} {
			lines[3+i] = `var w = "` + strings.Repeat("ab", (n-10)/2) + strings.Repeat("c", (n-10)%2) + `"`
		}
		if n := perFrame(t, "main.go", lines, diffview.Highlight("main.go", lines)); n > budget {
			t.Errorf("a step through a small Go file allocated %d KiB a frame, want at most %d KiB", n>>10, budget>>10)
		}
	})
}
