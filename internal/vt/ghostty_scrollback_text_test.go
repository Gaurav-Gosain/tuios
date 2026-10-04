//go:build ghostty

package vt

import (
	"fmt"
	"strings"
	"testing"
)

// TestGhosttyScrollbackTextMatchesTheLines holds the library's formatted
// history to the line-by-line read it replaces. Ways it could go wrong: the
// library drops or merges a line (a blank row, a soft-wrapped row), it writes
// a wide character, a tab or a combining mark differently, or the formatter
// path fails and the slow read is used without anyone noticing, or a trailing
// space with a style or a link is trimmed, which the formatter does to every
// trailing space.
func TestGhosttyScrollbackTextMatchesTheLines(t *testing.T) {
	term := NewWithScrollback(80, 24, 10000)
	g := term.(*GhosttyTerminal)
	defer g.Close()
	var b strings.Builder
	for i := range 3000 {
		switch i % 10 {
		case 0:
			fmt.Fprintf(&b, "\x1b[41mred %d   \x1b[0m\r\n", i)
		case 1:
			fmt.Fprintf(&b, "wide 日本語 %d\r\n", i)
		case 2:
			b.WriteString("\r\n")
		case 3:
			fmt.Fprintf(&b, "%s\r\n", strings.Repeat("x", 100))
		case 4:
			fmt.Fprintf(&b, "tab\there %d é\r\n", i)
		case 5:
			fmt.Fprintf(&b, "\x1b]8;;https://x.y/%d\x1b\\link  \x1b]8;;\x1b\\\r\n", i)
		case 6:
			fmt.Fprintf(&b, "\x1b[44mbg carried %d\r\nonto the next row\x1b[0m\r\n", i)
		case 7:
			fmt.Fprintf(&b, "\x1b[7m \x1b[0m  plain after %d   \r\n", i)
		case 8:
			fmt.Fprintf(&b, "\x1b[01;34mdir%d\x1b[0m\r\n", i)
		default:
			fmt.Fprintf(&b, "line %d\r\n", i)
		}
	}
	// Blank rows at the end of the history.
	b.WriteString(strings.Repeat("\r\n", 30))
	if _, err := g.Write([]byte(b.String())); err != nil {
		t.Fatal(err)
	}

	g.mu.Lock()
	n := g.scrollbackLenLocked()
	var want []string
	for i := range n {
		want = append(want, g.readHistoryLineLocked(g.term, i).String())
	}
	_, ok := g.historyTextLocked(g.term, n)
	g.mu.Unlock()
	if !ok {
		t.Fatalf("the library's text of %d history lines was not used", n)
	}

	var sb strings.Builder
	g.AppendScrollbackText(&sb)
	got := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("AppendScrollbackText wrote %d lines, the history has %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}
