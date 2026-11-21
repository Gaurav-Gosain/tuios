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
	skipAllocCountsUnderRace(t)
	m := keystrokeOS(t, 1, 200, 60)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())

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
