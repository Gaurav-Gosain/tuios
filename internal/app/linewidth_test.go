package app

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// lineWidth replaces ansi.StringWidth on the compositor's hot path, so the only
// thing that makes it safe is that it returns the same number. These tests and
// the fuzz target below are the entire justification for the fast path: if it
// ever disagrees, the window width is wrong, and a wrong window width is what
// discarded whole panes in the clipWindowContent bug.
func TestLineWidthMatchesReference(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"ascii", "hello world"},
		{"spaces", "   "},
		{"sgr-wrapped", "\x1b[38;5;12mhello\x1b[m"},
		{"sgr-only", "\x1b[m"},
		{"sgr-reset-long", "\x1b[0;1;38;2;255;0;0mx\x1b[0m"},
		{"osc-bel", "\x1b]0;title\x07text"},
		{"osc-st", "\x1b]0;title\x1b\\text"},
		{"tab", "a\tb"},
		{"del", "a\x7fb"},
		{"control", "a\x01b"},
		{"wide-cjk", "日本語"},
		{"wide-mixed", "ab日本語cd"},
		{"emoji", "hi 👋 there"},
		{"emoji-zwj", "family 👨‍👩‍👧‍👦 here"},
		{"combining", "éclair"},
		{"styled-cjk", "\x1b[31m日本\x1b[m"},
		{"unterminated-csi", "\x1b[38;5"},
		{"bare-escape", "\x1bX"},
		{"dcs", "\x1bP1$r\x1b\\text"},
		{"newline-free-long", "0123456789012345678901234567890123456789"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := ansi.StringWidth(tc.in)
			got := lineWidth(tc.in)
			if got != want {
				t.Fatalf("lineWidth(%q) = %d, ansi.StringWidth = %d", tc.in, got, want)
			}
		})
	}
}

// FuzzLineWidthMatchesReference is the real guard. The hand-written cases above
// only cover shapes that were thought of; this asserts equivalence on whatever
// the fuzzer reaches, including malformed escapes and partial UTF-8, which is
// exactly the input a misbehaving guest program can put on screen.
func FuzzLineWidthMatchesReference(f *testing.F) {
	seeds := []string{
		"",
		"hello",
		"\x1b[38;5;12mhello\x1b[m",
		"\x1b]0;t\x07x",
		"日本語",
		"👨‍👩‍👧‍👦",
		"é",
		"\x1b[",
		"\x1b",
		"\x1bP1$r\x1b\\",
		"a\x7f\x01\tb",
		"\xff\xfe",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		want := ansi.StringWidth(s)
		got := lineWidth(s)
		if got != want {
			t.Fatalf("lineWidth(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	})
}

// TestFramesWidthUsesWidestLine pins the property the clipWindowContent fix
// depends on: a frame whose first line is empty is still measured at its real
// width. Measuring lines[0] alone reported zero here, and the offscreen guard
// then discarded the leftmost tile entirely.
func TestFramesWidthUsesWidestLine(t *testing.T) {
	lines := []string{"", "short", "a much longer line here", "mid"}
	want := ansi.StringWidth("a much longer line here")
	if got := framesWidth(lines); got != want {
		t.Fatalf("framesWidth = %d, want %d", got, want)
	}

	if got := framesWidth([]string{"", "", ""}); got != 0 {
		t.Fatalf("framesWidth(all empty) = %d, want 0", got)
	}
}
