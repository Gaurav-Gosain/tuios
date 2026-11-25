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
	baseOf := func(e vt.Terminal) int64 {
		b, ok := e.ScrollbackBase()
		if !ok {
			t.Fatal("the emulator does not number its history")
		}
		return b
	}
	end := func(e vt.Terminal) int64 { return baseOf(e) + int64(e.ScrollbackLen()) }
	text := func(e vt.Terminal, n int64) string {
		var b strings.Builder
		for _, c := range e.ScrollbackLine(int(n - baseOf(e))) {
			b.WriteString(c.Content)
		}
		return strings.TrimRight(b.String(), " ")
	}
	check := func(step string, dst vt.Terminal) {
		t.Helper()
		if end(dst) != end(src) {
			t.Fatalf("%s: the history ends at %d here and at %d at the source", step, end(dst), end(src))
		}
		for n := baseOf(dst); n < end(dst); n++ {
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
	for before := baseOf(dst); ; {
		page, finish := CaptureHistoryPage(src, before, 64)
		finish()
		if page.Len() == 0 {
			if before != baseOf(src) {
				t.Fatalf("an empty page before line %d, and the source's oldest is %d", before, baseOf(src))
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
	if got := baseOf(keep); got < 77 {
		t.Fatalf("a snapshot without numbers set the base to %d, want it kept from 77", got)
	}

	// A daemon that restarts restores the pane from its saved history, and
	// the restored lines keep their numbers.
	saved := CaptureHistory(src, 50).State(50)
	restored := vt.NewWithScrollback(30, 4, 500)
	if err := ApplyHistory(restored, saved); err != nil {
		t.Fatal(err)
	}
	// Rows past the source's history were on its screen, which the restore
	// puts into the history.
	for n := baseOf(restored); n < min(end(restored), end(src)); n++ {
		if got, want := text(restored, n), text(src, n); got != want {
			t.Fatalf("restored line %d is %q, the source held %q", n, got, want)
		}
	}

	// A client that held more history than the restored pane keeps it. The
	// lines the pane no longer holds get numbers below the pane's oldest, and
	// a page for them is empty.
	if err := Apply(dst, Capture(restored, 30, 4, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if got := baseOf(dst) + int64(dst.ScrollbackLen()); got != end(restored) {
		t.Fatalf("the client's history ends at %d, the restored pane's at %d", got, end(restored))
	}
	big := vt.NewWithScrollback(30, 4, 1000)
	if err := Apply(big, Capture(src, 30, 4, 1000, 0)); err != nil {
		t.Fatal(err)
	}
	fresh := vt.NewWithScrollback(30, 4, 500)
	if err := ApplyHistory(fresh, func() *State {
		st := CaptureHistory(src, 20).State(20)
		st.ScrollbackNumbered, st.ScrollbackBase = false, 0
		return st
	}()); err != nil {
		t.Fatal(err)
	}
	if err := Apply(big, Capture(fresh, 30, 4, 0, big.ScrollbackLen())); err != nil {
		t.Fatal(err)
	}
	if b := baseOf(big); b >= 0 {
		t.Fatalf("a client that holds more history than its source numbered has base %d, want below zero", b)
	}
	page, finish := CaptureHistoryPage(fresh, baseOf(big)+10, 64)
	finish()
	if page.Len() != 0 {
		t.Fatalf("a page of lines the source never held has %d lines", page.Len())
	}
}
