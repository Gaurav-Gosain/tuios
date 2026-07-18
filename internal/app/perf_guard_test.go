package app

import (
	"strings"
	"testing"
)

// Benchmarks do not fail a build, so they cannot stop a performance
// regression from being merged. These guards do: they pin the allocation
// count of the render hot paths with testing.AllocsPerRun, which is
// deterministic in a way wall time is not, so they hold up on a loaded
// machine where a timing assertion would flake.
//
// Each ceiling below is the measured count plus a small margin. When one
// trips, the fix is to find the new allocation, not to raise the number: the
// counts here are the ones the paths were profiled at, and per-frame
// allocation was a recurring finding in review.

// allocsAtMost runs fn under testing.AllocsPerRun and fails when it allocates
// more than limit times per call.
func allocsAtMost(t *testing.T, name string, limit float64, fn func()) {
	t.Helper()
	// One warm-up call outside the measurement so first-call lazy setup (style
	// cache fill, builder pool priming) is not charged to the average.
	fn()
	got := testing.AllocsPerRun(50, fn)
	if got > limit {
		t.Errorf("%s: %.0f allocs/op, want <= %.0f", name, got, limit)
	}
	t.Logf("%s: %.0f allocs/op (limit %.0f)", name, got, limit)
}

// TestRenderCachedPathIsAllocationFree pins the most important invariant in the
// render loop. On a typical frame most windows are clean, so they take the
// cached branch of renderTerminal, and that branch returning a stored string
// must not allocate at all. If it ever does, the cost is paid once per clean
// window per frame, which at 60fps with a handful of windows is the difference
// between an idle multiplexer and one that keeps the garbage collector busy.
func TestRenderCachedPathIsAllocationFree(t *testing.T) {
	win := benchWindow(t, "guard-cached", realCols, realRows)
	m := newTestOS(win)

	// Prime the cache.
	win.MarkContentDirty()
	if got := m.renderTerminal(win, false, false); got == "" {
		t.Fatal("priming render returned empty content")
	}
	if win.ContentDirty {
		t.Fatal("content still dirty after priming render, cached path will not be taken")
	}

	allocsAtMost(t, "renderTerminal/cached", 0, func() {
		_ = m.renderTerminal(win, false, false)
	})
}

// TestIsBlankRenderIsAllocationFree pins the blank-frame guard, which
// cacheRender runs on every freshly rendered frame. It walks bytes and must
// never build a string to do it.
func TestIsBlankRenderIsAllocationFree(t *testing.T) {
	frame := renderedFrame(t, realCols, realRows)
	blank := strings.Repeat(strings.Repeat(" ", realCols)+"\n", realRows)

	allocsAtMost(t, "isBlankRender/typical", 0, func() {
		_ = isBlankRender(frame)
	})
	allocsAtMost(t, "isBlankRender/blank", 0, func() {
		_ = isBlankRender(blank)
	})
}

// TestLineWidthIsAllocationFree pins the ASCII fast path of the width
// measurement. Falling back to ansi.StringWidth is a correctness decision and
// stays available, but the fast path itself must not allocate, since it runs
// once per line per redrawn window per frame.
func TestLineWidthIsAllocationFree(t *testing.T) {
	line := "\x1b[38;5;12m" + strings.Repeat("content ", 25) + "\x1b[m"

	allocsAtMost(t, "lineWidth/ascii", 0, func() {
		_ = lineWidth(line)
	})

	lines := strings.Split(renderedFrame(t, realCols, realRows), "\n")
	allocsAtMost(t, "framesWidth/frame", 0, func() {
		_ = framesWidth(lines)
	})
}

// TestLineWidthFastPathCoversRenderedFrames is the guard that actually protects
// the width measurement's speed, which the allocation guards above cannot: the
// reference implementation does not allocate either, so a change that narrowed
// the fast path until ordinary rendered output stopped matching it would cost
// roughly five times the CPU per frame with every allocation assertion still
// green. That was verified, not assumed, by reverting lineWidth to call
// ansi.StringWidth unconditionally and watching the allocation guards pass.
//
// A rendered frame at the real host size is ASCII text and SGR escapes, which
// is exactly what the fast path exists to handle, so measuring one must not
// fall back even once.
func TestLineWidthFastPathCoversRenderedFrames(t *testing.T) {
	lines := strings.Split(renderedFrame(t, realCols, realRows), "\n")

	before := LineWidthFallbackCount()
	if got := framesWidth(lines); got == 0 {
		t.Fatal("frame measured as zero wide")
	}
	if n := LineWidthFallbackCount() - before; n != 0 {
		t.Errorf("measuring a rendered frame fell back to the reference implementation %d times, want 0", n)
	}

	// The fallback must still be reachable, otherwise the counter is proving
	// nothing and the non-ASCII correctness path has been lost.
	before = LineWidthFallbackCount()
	if got, want := lineWidth("日本語"), 6; got != want {
		t.Errorf("lineWidth(CJK) = %d, want %d", got, want)
	}
	if n := LineWidthFallbackCount() - before; n != 1 {
		t.Errorf("non-ASCII line took the fast path (%d fallbacks), want 1", n)
	}
}

// TestClipWindowContentAllocations pins the compositor's per-window clip. The
// fully visible case is the common one and joins the lines back into a single
// string, so it has a small fixed cost that must not start scaling with the
// number of lines.
func TestClipWindowContentAllocations(t *testing.T) {
	frame := renderedFrame(t, realCols, realRows)

	// strings.Split of the frame plus the join of the result.
	allocsAtMost(t, "clipWindowContent/fully-visible", 3, func() {
		_, _, _ = clipWindowContent(frame, 0, 0, realCols, realRows)
	})

	// Rejected on the cheap axes before the width scan, so only the split.
	allocsAtMost(t, "clipWindowContent/offscreen-right", 2, func() {
		_, _, _ = clipWindowContent(frame, realCols+10, 0, realCols, realRows)
	})
}

// TestRenderTerminalAllocationsDoNotScaleWithCells is a ceiling rather than an
// exact pin, because a full re-render legitimately allocates as it builds the
// frame. What it rules out is the failure mode that matters: an allocation
// introduced per cell rather than per line. At 207x55 a per-cell allocation
// would land somewhere near eleven thousand, so a ceiling set a little above
// the per-line count separates the two cases unambiguously.
func TestRenderTerminalAllocationsDoNotScaleWithCells(t *testing.T) {
	const cells = realCols * realRows

	t.Run("unfocused", func(t *testing.T) {
		win := benchWindow(t, "guard-alloc-u", realCols, realRows)
		m := newTestOS(win)
		allocsAtMost(t, "renderTerminal/unfocused", 400, func() {
			win.MarkContentDirty()
			_ = m.renderTerminal(win, false, false)
		})
	})

	t.Run("focused", func(t *testing.T) {
		win := benchWindow(t, "guard-alloc-f", realCols, realRows)
		m := newTestOS(win)
		allocsAtMost(t, "renderTerminal/focused", 600, func() {
			win.MarkContentDirty()
			_ = m.renderTerminal(win, true, true)
		})
	})

	if cells < 600 {
		t.Fatalf("guard ceilings assume a grid far larger than %d cells", cells)
	}
}
