// Ported from pele tests/render/break-hints.test.ts at 8aff183be57021b533f319a29341a529be934b08.
// The last case, which renders a sequence diagram, is covered by testdata/extra in the root package.

package text

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	zwsp = "\u200b"
	shy  = "\u00ad"
)

func TestBreakHints(t *testing.T) {
	t.Run("breaks at zero-width spaces instead of keeping the token whole", func(t *testing.T) {
		var out []string
		for _, line := range cjkLines("RRCConnection"+zwsp+"Reconfiguration"+zwsp+"Complete", 140) {
			out = append(out, strings.Replace(line, zwsp, "", 1))
		}
		if want := []string{"RRCConnection", "Reconfiguration", "Complete"}; !slices.Equal(out, want) {
			t.Errorf("got %q", out)
		}
	})

	t.Run("shows a soft hyphen as a hyphen only where the line breaks", func(t *testing.T) {
		source := "RRCConnection" + shy + "Reconfiguration" + shy + "Complete"
		if out, want := cjkLines(source, 140), []string{"RRCConnection-", "Reconfiguration-", "Complete"}; !slices.Equal(out, want) {
			t.Errorf("got %q", out)
		}
		for _, w := range LayoutLabel(source, false, MetricsMeasurer, 16, 140, 0).Widths {
			if w > 140 {
				t.Errorf("line of width %v", w)
			}
		}
		if out, want := cjkLines("Re"+shy+"con"+shy+"fig", 200), []string{"Re" + shy + "con" + shy + "fig"}; !slices.Equal(out, want) {
			t.Errorf("got %q", out)
		}
	})

	t.Run("keeps a soft-hyphenated word on the line when the rest of it fits there", func(t *testing.T) {
		if out, want := cjkLines("a uni"+shy+"t test", 39.2), []string{"a uni" + shy + "t", "test"}; !slices.Equal(out, want) {
			t.Errorf("got %q", out)
		}
	})

	for _, raw := range []string{"a uni" + shy + "t" + shy + "ed", "a uni" + shy + "<b>t</b> test", "a uni" + shy + " t test"} {
		t.Run("keeps every line within the width: "+raw, func(t *testing.T) {
			for _, w := range LayoutLabel(raw, false, MetricsMeasurer, 16, 39.2, 0).Widths {
				if w > 39.2 {
					t.Errorf("line of width %v", w)
				}
			}
		})
	}

	t.Run("breaks at a space after a soft hyphen without showing the hyphen", func(t *testing.T) {
		if out := strings.Join(cjkLines("a uni"+shy+" t test", 39.2), " "); strings.Contains(out, "-") {
			t.Errorf("got %q", out)
		}
	})

	t.Run("shows no hyphen where a line breaks at a space in the next styled span", func(t *testing.T) {
		if out, want := cjkLines("ab"+shy+"<i> cd</i>", 25), []string{"ab" + shy, "cd"}; !slices.Equal(out, want) {
			t.Errorf("got %q", out)
		}
	})

	t.Run("reads &shy; as a soft hyphen", func(t *testing.T) {
		if out, want := cjkLines("RRCConnection&shy;Reconfiguration&shy;Complete", 140), []string{"RRCConnection-", "Reconfiguration-", "Complete"}; !slices.Equal(out, want) {
			t.Errorf("got %q", out)
		}
	})

	t.Run("gives soft hyphens no width when they do not break", func(t *testing.T) {
		if MetricsMeasurer.Width("ab"+shy+"c", 16, 0) != MetricsMeasurer.Width("abc", 16, 0) {
			t.Error("a soft hyphen has width")
		}
	})

	// pele bounds the time. The bound here is on bytes allocated, which does not
	// depend on the speed of the machine: a quadratic wrap copies gigabytes.
	t.Run("wraps many pieces without width in linear time", func(t *testing.T) {
		for _, hint := range []string{shy, zwsp} {
			raw := strings.Repeat("a", 22) + strings.Repeat("̀"+hint, 24000) + "i xx"
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			LayoutLabel(raw, false, MetricsMeasurer, 16, 200, 0)
			runtime.ReadMemStats(&after)
			if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
				t.Errorf("hint %q: %d MB allocated", hint, got>>20)
			}
		}
	})
}
