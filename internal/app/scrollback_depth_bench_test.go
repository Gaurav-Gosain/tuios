package app

// What a full scrollback costs the pane in front of it.
//
// Every performance number this repo holds was taken on a pane that had just
// started: a fresh emulator, an empty history, a burst of output measured over
// a few seconds. A pane the maintainer actually works in has been open for
// hours and is holding its whole scrollback cap, and nothing here had ever
// asked whether that changes the cost of drawing it.
//
// There are two questions and they have different answers. Drawing the live
// tail should cost the same whatever is behind it, because the visible grid is
// the same size either way; if it does not, the history is being walked on
// every frame of normal use. Drawing a scrolled-back view should cost the same
// wherever in the history it is looking, because a line is fetched by index; if
// it does not, the buffer is a list being walked from one end and scrolling to
// the top of a deep history costs more than scrolling to the bottom of it.
//
// Benchmark only, so a normal CI test run pays nothing. Filling the deepest
// case takes a couple of seconds of setup, which is why the depths stop where
// they do: 50k lines is already past the default cap.

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// fillHistory pushes n lines of styled output through a window's emulator so
// they land in its history. The lines carry colour so a scrollback line costs
// what a real one does rather than what a run of spaces does.
func fillHistory(tb testing.TB, win *terminal.Window, n, cols int) {
	tb.Helper()
	var buf bytes.Buffer
	buf.Grow(n * (cols + 24))
	for i := range n {
		fmt.Fprintf(&buf, "\x1b[38;5;%dmline %d ", 8+i%200, i)
		for buf.Len()%(cols/2) != 0 {
			buf.WriteByte('.')
		}
		buf.WriteString("\x1b[m\r\n")
	}
	win.LockIO()
	_, _ = win.Terminal.Write(buf.Bytes())
	win.UnlockIO()
}

// scrollbackWindow builds a pane holding depth lines of history.
func scrollbackWindow(tb testing.TB, id string, depth, cols, rows int) *terminal.Window {
	tb.Helper()
	win := newTestWindow(tb, id, cols+2, rows+2)
	win.SetScrollbackMaxLines(100000)
	if depth > 0 {
		fillHistory(tb, win, depth, cols)
	}
	return win
}

// BenchmarkScrollbackDepth measures the frame in front of a history, at the
// live tail and scrolled into the middle of it.
//
// "tail" is the case that matters most, because it is every frame of ordinary
// use: a pane with a full history that the user is not scrolling. It should be
// flat across depth.
//
// "scrolled" puts the viewport halfway up the history, which is the case that
// would expose a buffer that has to be walked to be indexed. It should also be
// flat across depth, and a rising line here means scrolling into a long history
// gets slower the further back it goes.
func BenchmarkScrollbackDepth(b *testing.B) {
	const cols, rows = 158, 40
	for _, depth := range []int{0, 1000, 10000, 50000} {
		win := scrollbackWindow(b, fmt.Sprintf("sb-%d", depth), depth, cols, rows)
		m := newTestOS(win)
		m.Mode = TerminalMode

		b.Run(fmt.Sprintf("depth-%d/tail", depth), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				win.MarkContentDirty()
				_ = m.renderTerminal(win, true, true)
			}
		})

		if depth == 0 {
			continue
		}
		b.Run(fmt.Sprintf("depth-%d/scrolled", depth), func(b *testing.B) {
			win.EnterScrollbackMode()
			win.ScrollUp(depth / 2)
			defer win.ExitScrollbackMode()
			b.ReportAllocs()
			for b.Loop() {
				win.MarkContentDirty()
				_ = m.renderTerminal(win, true, true)
			}
		})
	}
}

// BenchmarkScrollbackFetch isolates the indexing question from the drawing.
// renderTerminal draws a screenful whatever it is handed, so a cost that grows
// with depth would be diluted there; this asks the buffer directly for a line
// at each end of a deep history and at the middle of it.
func BenchmarkScrollbackFetch(b *testing.B) {
	const cols, rows = 158, 40
	win := scrollbackWindow(b, "sb-fetch", 50000, cols, rows)
	n := win.ScrollbackLen()
	if n == 0 {
		b.Fatal("the history is empty")
	}
	for _, at := range []struct {
		name  string
		index int
	}{
		{"oldest", 0},
		{"middle", n / 2},
		{"newest", n - 1},
	} {
		b.Run(at.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = win.ScrollbackLine(at.index)
			}
		})
	}
}

// TestScrollbackCapHolds is what makes the benchmark honest about depth: if the
// buffer silently dropped everything past a smaller cap, every "deep" case
// above would be measuring a shallow one.
func TestScrollbackCapHolds(t *testing.T) {
	win := scrollbackWindow(t, "sb-cap", 20000, 158, 40)
	if got := win.ScrollbackLen(); got < 15000 {
		t.Errorf("20000 lines of output left %d in the history; the depth cases are not deep", got)
	}
}
