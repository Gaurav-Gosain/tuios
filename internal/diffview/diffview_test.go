package diffview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHighlightLeavesUnknownAndHugeTextPlain(t *testing.T) {
	if Highlight("notes.unknownext", []string{"func x"}) != nil {
		t.Error("a file of no known type was highlighted")
	}
	if Highlight("big.go", []string{strings.Repeat("x", maxHighlightLine+1)}) != nil {
		t.Error("a line past the limit was tokenised")
	}
	if Enabled && Highlight("run", []string{"#!/bin/sh", "echo hi"}) == nil {
		t.Error("a script with a #! line was not highlighted")
	}
}

func TestCodeRangesPadsByCellsNotBytes(t *testing.T) {
	th := NewTheme(Palette{})
	cases := []struct {
		name, text string
		xOff       int
		want       string
	}{
		{"ascii", "abc", 0, "abc       "},
		{"two-byte letter", "caf\u00e9", 0, "caf\u00e9      "},
		{"wide letter", "a\u4e2db", 0, "a\u4e2db      "},
		{"DEL byte", "a\x7fb", 0, "a\x7fb        "},
		{"negative offset", "abc", -3, "abc       "},
	}
	for _, c := range cases {
		got := ansi.Strip(th.CodeRanges(c.text, nil, nil, Context, false, c.xOff, 10))
		if got != c.want {
			t.Errorf("%s: drew %q in 10 cells, want %q", c.name, got, c.want)
		}
	}
}
