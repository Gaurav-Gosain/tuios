package app

import (
	"strings"
	"testing"
	"time"
)

// FuzzCockpitMarkdown lays out arbitrary text as an agent's message, the
// way the thread draws it. Its output is bounded by its input: lines and
// runs grow at most linearly, no line is wider than the thread past a list
// mark, and a text at the size bound lays out well inside a frame's budget.
// The seeds hold the shapes that once grew without bound: a wide table with
// many short rows, and one long word.
func FuzzCockpitMarkdown(f *testing.F) {
	for _, seed := range []string{
		"**Navigate**\n- Read `vt/`\n  1. nested\n\n```go\nfunc a() {}\n```\n\n| a | b |\n|-|-:|\n| `x` | [l](https://e.com) |\n",
		strings.Repeat("|a", 4000) + "\n" + strings.Repeat("|-", 4000) + "\n" + strings.Repeat("|\n", 8000),
		"|" + strings.Repeat("x", 16000) + "|b|\n|-|-|\n|c|d|\n",
		strings.Repeat("x", 32000),
		"`" + strings.Repeat("a ", 16000) + "`",
		strings.Repeat("> - ", 40) + "deep",
		"❤️ 👩‍💻 é " + strings.Repeat("界", 100),
	} {
		f.Add(seed)
	}
	m := &OS{}
	l := m.cockpitLook()
	f.Fuzz(func(t *testing.T, src string) {
		for _, width := range []int{12, 40, 120} {
			start := time.Now()
			lines := m.ckMarkdown(l, src, width)
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("laying out %d bytes at %d took %v", len(src), width, d)
			}
			segs := 0
			for _, ln := range lines {
				segs += len(ln.segs)
				if w := ckSegsWidth(ln.segs) + ckSegsWidth(ln.right); w > width+12 {
					t.Fatalf("a line is %d cells at width %d: %q", w, width, ln.segs)
				}
			}
			if len(lines) > 4*len(src)+64 || segs > 16*len(src)+4096 {
				t.Fatalf("%d bytes laid out as %d lines and %d runs at width %d", len(src), len(lines), segs, width)
			}
		}
	})
}
