package vt_test

// Conformance for history line numbers (Terminal.ScrollbackBase). A client
// keeps less history than its daemon and asks the daemon for older lines by
// number, so the two emulators must give each line the same number although
// each drops lines at its own limit.
//
// Ways it can go wrong, each covered below:
//   - a ring at its limit drops a line and does not count it;
//   - a clear, or a smaller limit, drops lines and does not count them;
//   - a reflow joins the rows of a wrapped line and splits them again, and
//     a smaller ring that dropped the start of the line lays out fewer rows,
//     so the numbers of every later line differ;
//   - a taller screen takes rows back from the history, more from a larger
//     ring, and the numbers of every later line differ.

import (
	"fmt"
	"strings"
	"testing"

	"tuios.dev/vt"
)

// numbered is the text of every history line of e by its number. Leading
// rows of a line whose start e dropped are left out: a reflow lays that line
// out from what e holds of it, which another emulator lays out whole.
func numbered(e vt.Terminal) map[int64]string {
	out := map[int64]string{}
	base, ok := e.ScrollbackBase()
	if !ok {
		return out
	}
	start := 0
	if base > 0 {
		for start < e.ScrollbackLen() {
			wrapped, _ := e.ScrollbackSoftWrapped(start)
			start++
			if !wrapped {
				break
			}
		}
	}
	for i := start; i < e.ScrollbackLen(); i++ {
		var b strings.Builder
		for _, c := range e.ScrollbackLine(i) {
			b.WriteString(c.Content)
		}
		out[base+int64(i)] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// cursorNumber is the number of e's cursor row.
func cursorNumber(e vt.Terminal) int64 {
	base, _ := e.ScrollbackBase()
	return base + int64(e.ScrollbackLen()+e.CursorPosition().Y)
}

// sameNumbers fails when small and big give their cursor rows different
// numbers, or give a line both hold different text.
func sameNumbers(t *testing.T, step string, small, big vt.Terminal) {
	t.Helper()
	if s, b := cursorNumber(small), cursorNumber(big); s != b {
		t.Fatalf("%s: the cursor row is number %d in the small emulator and %d in the big one", step, s, b)
	}
	want, have := numbered(big), numbered(small)
	shared := 0
	for n, got := range have {
		if w, ok := want[n]; ok {
			shared++
			if w != got {
				t.Fatalf("%s: line %d is %q in the small emulator and %q in the big one", step, n, got, w)
			}
		}
	}
	if shared == 0 && len(have) > 0 {
		t.Fatalf("%s: the two emulators share no line number", step)
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
	if _, ok := vt.NewWithScrollback(1, 1, 1).ScrollbackBase(); !ok {
		t.Skip("this backend does not number its history")
	}
	t.Run("emulators with different limits number lines alike", func(t *testing.T) {
		small, big := vt.NewWithScrollback(30, 4, 5), vt.NewWithScrollback(30, 4, 500)
		both := []vt.Terminal{small, big}
		feedAll(both, lines(0, 40, 0))
		if base, _ := small.ScrollbackBase(); base == 0 {
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
		base, _ := big.ScrollbackBase()
		end := base + int64(big.ScrollbackLen())
		for _, e := range both {
			e.ClearScrollback()
			if got, _ := e.ScrollbackBase(); got != end {
				t.Fatalf("after a clear the oldest line is number %d, want %d", got, end)
			}
		}
		feedAll(both, lines(20, 30, 0))
		sameNumbers(t, "after a clear and 10 lines", small, big)
	})

	t.Run("a smaller limit counts the lines it drops", func(t *testing.T) {
		e := vt.NewWithScrollback(30, 4, 100)
		feedAll([]vt.Terminal{e}, lines(0, 30, 0))
		want := cursorNumber(e)
		e.SetScrollbackMaxLines(10)
		if got := cursorNumber(e); got != want {
			t.Fatalf("the cursor row is number %d after a smaller limit, want %d", got, want)
		}
	})

	t.Run("a resize keeps the number of the cursor row", func(t *testing.T) {
		e := vt.NewWithScrollback(20, 4, 500)
		feedAll([]vt.Terminal{e}, lines(0, 30, 12))
		want := cursorNumber(e)
		for _, size := range [][2]int{{10, 4}, {30, 6}, {20, 12}, {7, 3}} {
			e.Resize(size[0], size[1])
			if got := cursorNumber(e); got != want {
				t.Fatalf("after a resize to %dx%d the cursor row is number %d, want %d", size[0], size[1], got, want)
			}
		}
	})

	t.Run("a reflow of a line whose start the small ring dropped", func(t *testing.T) {
		// One line of 200 columns, still being typed, wraps over ten rows.
		// The small ring holds only its last rows, so a reflow joins less
		// of it there than in the big ring, and lays it out in fewer rows.
		small, big := vt.NewWithScrollback(20, 4, 3), vt.NewWithScrollback(20, 4, 500)
		both := []vt.Terminal{small, big}
		feedAll(both, lines(0, 30, 0)+strings.Repeat("y", 200))
		for _, size := range [][2]int{{30, 4}, {13, 5}} {
			for _, e := range both {
				e.Resize(size[0], size[1])
			}
			sameNumbers(t, fmt.Sprintf("after a resize to %dx%d", size[0], size[1]), small, big)
		}
		feedAll(both, "\r\n"+lines(30, 40, 0))
		sameNumbers(t, "after the resizes and 10 lines", small, big)
	})

	t.Run("a taller screen takes back more rows from the big ring", func(t *testing.T) {
		small, big := vt.NewWithScrollback(20, 4, 2), vt.NewWithScrollback(20, 4, 500)
		both := []vt.Terminal{small, big}
		feedAll(both, lines(0, 30, 0))
		for _, e := range both {
			e.Resize(20, 12)
		}
		sameNumbers(t, "after a resize to 20x12", small, big)
		feedAll(both, lines(30, 50, 0))
		sameNumbers(t, "after a resize to 20x12 and 20 lines", small, big)
	})

	t.Run("the epoch changes when a resize moves the numbers", func(t *testing.T) {
		e := vt.NewWithScrollback(20, 4, 10)
		feedAll([]vt.Terminal{e}, lines(0, 30, 12))
		epoch := e.ScrollbackEpoch()
		// A full ring drops a line for each it takes: no number moves.
		feedAll([]vt.Terminal{e}, lines(30, 40, 12))
		if e.ScrollbackEpoch() != epoch {
			t.Fatal("drops from the old end changed the epoch")
		}
		// The same base again renumbers nothing, and changes nothing.
		base, _ := e.ScrollbackBase()
		gen := e.ScrollbackGeneration()
		e.SetScrollbackBase(base)
		if e.ScrollbackEpoch() != epoch || e.ScrollbackGeneration() != gen {
			t.Fatal("setting the base it already has changed the epoch or the generation")
		}
		// A taller screen takes rows back from the history: the numbers stay.
		e.Resize(20, 8)
		if e.ScrollbackEpoch() != epoch {
			t.Fatal("a taller screen that only took rows back changed the epoch")
		}
		// A narrower screen wraps the screen's lines into more rows, and the
		// history is numbered back from the cursor row.
		before, _ := e.ScrollbackBase()
		e.Resize(10, 8)
		after, _ := e.ScrollbackBase()
		if after == before {
			t.Fatal("the narrowing did not move the numbers, so the case tests nothing")
		}
		if e.ScrollbackEpoch() == epoch {
			t.Fatal("a resize moved the numbers and kept the epoch")
		}
	})
}
