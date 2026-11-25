package snapshot

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// A snapshot carries the history's numbering (State.ScrollbackBase), so a
// client emulator built from it, which keeps less history than its daemon,
// gives each line the number the daemon's gives it. Ways it can go wrong:
//   - a fresh emulator starts its numbering at zero;
//   - an emulator that already holds history and is handed only the missing
//     lines keeps its own numbering, which drifted while it was away;
//   - a snapshot from a backend that does not count, or from an older
//     daemon, overwrites the emulator's numbering with zero.
func TestSnapshotCarriesHistoryNumbers(t *testing.T) {
	src := vt.NewWithScrollback(30, 4, 500)
	write := func(from, to int) {
		var b strings.Builder
		for i := from; i < to; i++ {
			fmt.Fprintf(&b, "line %d\r\n", i)
		}
		_, _ = src.Write([]byte(b.String()))
	}
	end := func(e vt.Terminal) int64 { return e.ScrollbackBase() + int64(e.ScrollbackLen()) }
	text := func(e vt.Terminal, n int64) string {
		var b strings.Builder
		for _, c := range e.ScrollbackLine(int(n - e.ScrollbackBase())) {
			b.WriteString(c.Content)
		}
		return strings.TrimRight(b.String(), " ")
	}
	check := func(step string, dst vt.Terminal) {
		t.Helper()
		if end(dst) != end(src) {
			t.Fatalf("%s: the history ends at %d here and at %d at the source", step, end(dst), end(src))
		}
		for n := dst.ScrollbackBase(); n < end(dst); n++ {
			if got, want := text(dst, n), text(src, n); got != want {
				t.Fatalf("%s: line %d is %q here and %q at the source", step, n, got, want)
			}
		}
	}

	// The source dropped lines of its own first, so its numbering does not
	// start at zero.
	write(0, 600)
	src.ClearScrollback()
	write(600, 640)

	dst := vt.NewWithScrollback(30, 4, 10)
	if err := Apply(dst, Capture(src, 30, 4, 0, 0)); err != nil {
		t.Fatal(err)
	}
	check("a fresh emulator", dst)

	// Away while the source moved on: handed only the missing lines.
	write(640, 700)
	if err := Apply(dst, Capture(src, 30, 4, 0, dst.ScrollbackLen())); err != nil {
		t.Fatal(err)
	}
	check("an emulator that held history", dst)

	// From there the same output to both keeps them in step.
	b := new(strings.Builder)
	for i := 700; i < 730; i++ {
		fmt.Fprintf(b, "line %d\r\n", i)
	}
	_, _ = src.Write([]byte(b.String()))
	_, _ = dst.Write([]byte(b.String()))
	check("the same output to both", dst)

	// The lines the emulator does not hold come from the source by number,
	// a page at a time, up to the source's oldest.
	for before := dst.ScrollbackBase(); ; {
		page, finish := CaptureHistoryPage(src, before, 64)
		finish()
		if page.Len() == 0 {
			if before != src.ScrollbackBase() {
				t.Fatalf("an empty page before line %d, and the source's oldest is %d", before, src.ScrollbackBase())
			}
			break
		}
		if got := page.First + int64(page.Len()); got != before {
			t.Fatalf("a page asked for lines before %d ends before %d", before, got)
		}
		next := page.First
		if err := page.Lines(dst, func(n int64, line uv.Line, _, _ bool) {
			var b strings.Builder
			for _, c := range line {
				b.WriteString(c.Content)
			}
			if got, want := strings.TrimRight(b.String(), " "), text(src, n); got != want {
				t.Fatalf("line %d of a page is %q, the source holds %q", n, got, want)
			}
			if n != next {
				t.Fatalf("a page gave line %d where %d was next", n, next)
			}
			next++
		}); err != nil {
			t.Fatal(err)
		}
		before = page.First
	}

	unnumbered := Capture(src, 30, 4, 0, 0)
	unnumbered.ScrollbackNumbered, unnumbered.ScrollbackBase = false, 0
	keep := vt.NewWithScrollback(30, 4, 10)
	keep.SetScrollbackBase(77)
	if err := Apply(keep, unnumbered); err != nil {
		t.Fatal(err)
	}
	if keep.ScrollbackBase() < 77 {
		t.Fatalf("a snapshot without numbers set the base to %d, want it kept from 77", keep.ScrollbackBase())
	}
}
