//go:build ghostty

package vt

import (
	"fmt"
	"testing"
)

// TestGhosttyScrollbackKeepsTheLinesAskedFor pins the byte budget handed to
// libghostty. Without it the library's default of 10 000 bytes bound the
// history first, and a 207-column pane kept about 400 lines of 10 000.
func TestGhosttyScrollbackKeepsTheLinesAskedFor(t *testing.T) {
	for _, cols := range []int{80, 207} {
		const want = 10000
		term := NewWithScrollback(cols, 24, want)
		for i := range 2 * want {
			_, _ = term.Write([]byte(fmt.Sprintf("%d\r\n", i)))
		}
		got := term.ScrollbackLen()
		term.Close()
		// libghostty prunes whole pages, so the count lands near the limit
		// rather than on it: 9555 and 9852 here, against 870 and 402 before.
		if got < want*9/10 || got > want*12/10 {
			t.Errorf("%d columns: %d scrollback lines held, want about %d", cols, got, want)
		}
	}
}

func TestGhosttyScrollCacheIsBounded(t *testing.T) {
	g := NewGhosttyTerminal(80, 24)
	defer g.Close()
	for i := range 2000 {
		_, _ = g.Write([]byte(fmt.Sprintf("%d\r\n", i)))
	}
	n := g.ScrollbackLen()
	if n < 1000 {
		t.Fatalf("only %d lines of history, the walk below proves nothing", n)
	}
	for i := range n {
		_ = g.ScrollbackLine(i)
	}
	if held := len(g.scrollCache); held > ghosttyScrollCacheCap {
		t.Fatalf("history cache holds %d decoded lines after a walk of %d, want at most %d", held, n, ghosttyScrollCacheCap)
	}
}
