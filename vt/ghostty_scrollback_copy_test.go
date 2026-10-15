//go:build ghostty

package vt

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// TestGhosttyScrollbackCopyMatchesTheLiveRead holds the copy made from a
// snapshot to the lines read from the live terminal. Ways it could go wrong:
// the snapshot keeps less history than the terminal or numbers it from
// another first line, a palette colour the guest set with OSC 4 is resolved
// against the theme, a wrap flag or a link is lost, or the copy taken while a
// full-screen program runs reads the alternate screen's empty history.
func TestGhosttyScrollbackCopyMatchesTheLiveRead(t *testing.T) {
	for _, alt := range []bool{false, true} {
		t.Run(fmt.Sprintf("alt=%v", alt), func(t *testing.T) {
			term := NewWithScrollback(40, 10, 5000)
			g := term.(*GhosttyTerminal)
			defer g.Close()
			var b strings.Builder
			b.WriteString("\x1b]4;3;rgb:12/34/56\x1b\\")
			for i := range 3000 {
				switch i % 5 {
				case 0:
					fmt.Fprintf(&b, "\x1b[33;44mpal %d\x1b[0m\r\n", i)
				case 1:
					fmt.Fprintf(&b, "\x1b[38;2;1;2;3mrgb\x1b[0m 日本 %d\r\n", i)
				case 2:
					fmt.Fprintf(&b, "%s\r\n", strings.Repeat("w", 95))
				case 3:
					fmt.Fprintf(&b, "\x1b]8;;https://example.com/%d\x1b\\link\x1b]8;;\x1b\\\r\n", i)
				default:
					b.WriteString("\r\n")
				}
			}
			if alt {
				b.WriteString("\x1b[?1049hfull screen")
			}
			if _, err := g.Write([]byte(b.String())); err != nil {
				t.Fatal(err)
			}
			n := g.ScrollbackLen()
			if n < 2000 {
				t.Fatalf("history holds %d lines, want the most of 3000", n)
			}
			from := n - 1500
			lazy := g.CopyScrollback(from, n)
			if lazy.build == nil {
				t.Fatal("the copy was read under the lock, not from a snapshot")
			}
			g.mu.Lock()
			eager := g.copyScrollbackLocked(from, n)
			if alt {
				// The eager read of the main history under the alternate
				// screen goes through the same decoded copy.
				eager = func() *ScrollbackCopy {
					src := g.altHistoryLocked()
					c := &ScrollbackCopy{}
					for i := from; i < n; i++ {
						c.push(g.readHistoryLineLocked(src, i), wrapFlag(ghosttyRowWrap(src, gh.Point{Tag: gh.PointTagHistory, Y: uint32(i)})), n-from)
					}
					return c
				}()
			}
			g.mu.Unlock()
			if lazy.Len() != eager.Len() || lazy.Len() != n-from {
				t.Fatalf("the copy holds %d lines, the live read %d, want %d", lazy.Len(), eager.Len(), n-from)
			}
			var want []uv.Line
			eager.Rows(0, eager.Len(), func(_ int, l uv.Line) bool { want = append(want, append(uv.Line(nil), l...)); return true })
			wraps := 0
			lazy.Rows(0, lazy.Len(), func(i int, l uv.Line) bool {
				if got, w := l.Render(), want[i].Render(); got != w {
					t.Errorf("line %d = %q, want %q", i, got, w)
				}
				if lazy.Wrapped(i) != eager.Wrapped(i) {
					t.Errorf("line %d wrapped = %v, want %v", i, lazy.Wrapped(i), eager.Wrapped(i))
				}
				if lazy.Wrapped(i) {
					wraps++
				}
				return !t.Failed()
			})
			if wraps == 0 {
				t.Error("no line of the copy is wrapped, so the flags were not compared")
			}
		})
	}
}
