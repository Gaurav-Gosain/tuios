package vt_test

// Conformance for history line numbers (Terminal.ScrollbackBase). A client
// keeps less history than its daemon and asks the daemon for older lines by
// number, so the two emulators must give each line the same number although
// each drops lines at its own limit.
//
// Ways it can go wrong, each covered below:
//   - a ring at its limit drops a line and does not count it;
//   - a clear, or a smaller limit, drops lines and does not count them;
//   - a reflow takes lines back from the history and pushes them again, and
//     the numbers of the older lines move;
//   - the two emulators agree on one size and disagree after a resize.

import (
	"fmt"
	"strings"
	"testing"

	"tuios.dev/vt"
)

// numbered is the text of every history line of e by its number.
func numbered(e vt.Terminal) map[int64]string {
	out := map[int64]string{}
	base := e.ScrollbackBase()
	for i := range e.ScrollbackLen() {
		var b strings.Builder
		for _, c := range e.ScrollbackLine(i) {
			b.WriteString(c.Content)
		}
		out[base+int64(i)] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// sameNumbers fails when small and big disagree on the number of their newest
// history line, or on the text of a line both hold.
func sameNumbers(t *testing.T, step string, small, big vt.Terminal) {
	t.Helper()
	endS := small.ScrollbackBase() + int64(small.ScrollbackLen())
	endB := big.ScrollbackBase() + int64(big.ScrollbackLen())
	if endS != endB {
		t.Fatalf("%s: the history ends at %d in the small emulator and %d in the big one", step, endS, endB)
	}
	want := numbered(big)
	for n, got := range numbered(small) {
		if w, ok := want[n]; !ok || w != got {
			t.Fatalf("%s: line %d is %q in the small emulator and %q in the big one", step, n, got, w)
		}
	}
}

func feedAll(es []vt.Terminal, s string) {
	for _, e := range es {
		_, _ = e.Write([]byte(s))
	}
}

func lines(from, to int, width int) string {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "line %d %s\r\n", i, strings.Repeat("x", width))
	}
	return b.String()
}

func TestConform_HistoryNumbers(t *testing.T) {
	t.Run("emulators with different limits number lines alike", func(t *testing.T) {
		small, big := vt.NewWithScrollback(30, 4, 5), vt.NewWithScrollback(30, 4, 500)
		both := []vt.Terminal{small, big}
		feedAll(both, lines(0, 40, 0))
		if small.ScrollbackBase() == 0 {
			t.Fatal("the small ring dropped lines at its limit and did not count them")
		}
		sameNumbers(t, "after 40 lines", small, big)
	})

	t.Run("a line keeps its number until it is dropped", func(t *testing.T) {
		e := vt.NewWithScrollback(30, 4, 100)
		feedAll([]vt.Terminal{e}, lines(0, 20, 0))
		before := numbered(e)
		feedAll([]vt.Terminal{e}, lines(20, 30, 0))
		after := numbered(e)
		for n, s := range before {
			if after[n] != s {
				t.Fatalf("line %d was %q and is %q", n, s, after[n])
			}
		}
	})

	t.Run("a clear counts the lines it drops", func(t *testing.T) {
		small, big := vt.NewWithScrollback(30, 4, 5), vt.NewWithScrollback(30, 4, 500)
		both := []vt.Terminal{small, big}
		feedAll(both, lines(0, 20, 0))
		end := big.ScrollbackBase() + int64(big.ScrollbackLen())
		for _, e := range both {
			e.ClearScrollback()
			if got := e.ScrollbackBase(); got != end {
				t.Fatalf("after a clear the oldest line is number %d, want %d", got, end)
			}
		}
		feedAll(both, lines(20, 30, 0))
		sameNumbers(t, "after a clear and 10 lines", small, big)
	})

	t.Run("a smaller limit counts the lines it drops", func(t *testing.T) {
		e := vt.NewWithScrollback(30, 4, 100)
		feedAll([]vt.Terminal{e}, lines(0, 30, 0))
		end := e.ScrollbackBase() + int64(e.ScrollbackLen())
		e.SetScrollbackMaxLines(10)
		if got := e.ScrollbackBase() + int64(e.ScrollbackLen()); got != end {
			t.Fatalf("the history ends at %d after a smaller limit, want %d", got, end)
		}
	})

	t.Run("a reflow keeps the numbers of the lines it leaves", func(t *testing.T) {
		small, big := vt.NewWithScrollback(20, 4, 8), vt.NewWithScrollback(20, 4, 500)
		both := []vt.Terminal{small, big}
		// Lines longer than the narrow width, so a narrowing wraps them.
		feedAll(both, lines(0, 30, 12))
		old := numbered(big)
		for _, size := range [][2]int{{10, 4}, {30, 6}, {20, 4}} {
			for _, e := range both {
				e.Resize(size[0], size[1])
			}
			sameNumbers(t, fmt.Sprintf("after a resize to %dx%d", size[0], size[1]), small, big)
		}
		now := numbered(big)
		// The oldest lines are far above what a reflow takes back.
		for n := big.ScrollbackBase(); n < big.ScrollbackBase()+10; n++ {
			if now[n] != old[n] {
				t.Fatalf("line %d was %q before the resizes and is %q after", n, old[n], now[n])
			}
		}
		feedAll(both, lines(30, 40, 12))
		sameNumbers(t, "after the resizes and 10 lines", small, big)
	})
}
