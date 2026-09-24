package review

import (
	"fmt"
	"strings"
	"testing"
)

func numbered(n int, at map[int]string) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	for k, v := range at {
		lines[k-1] = v
	}
	return lines
}

func TestAnchor(t *testing.T) {
	cases := []struct {
		name   string
		lines  []string
		line   int
		quote  string
		want   int
		wantOK bool
	}{
		{name: "in place", lines: numbered(10, map[int]string{5: "if err == nil {"}), line: 5, quote: "if err == nil {", want: 5, wantOK: true},
		{name: "moved down", lines: numbered(100, map[int]string{47: "if err == nil {"}), line: 42, quote: "if err == nil {", want: 47, wantOK: true},
		{name: "moved up", lines: numbered(100, map[int]string{30: "if err == nil {"}), line: 42, quote: "if err == nil {", want: 30, wantOK: true},
		{name: "trailing blanks and CR do not count", lines: numbered(10, map[int]string{3: "x := 1  \r"}), line: 3, quote: "x := 1", want: 3, wantOK: true},
		{name: "deleted", lines: numbered(100, nil), line: 42, quote: "if err == nil {", wantOK: false},
		{name: "duplicates take the nearest", lines: numbered(100, map[int]string{10: "}", 44: "}", 60: "}"}), line: 42, quote: "}", want: 44, wantOK: true},
		{name: "a tie takes the one above", lines: numbered(100, map[int]string{40: "}", 44: "}"}), line: 42, quote: "}", want: 40, wantOK: true},
		{name: "past the window, the nearest in the file", lines: numbered(300, map[int]string{250: "moved far"}), line: 10, quote: "moved far", want: 250, wantOK: true},
		{name: "no quote keeps its line", lines: numbered(10, nil), line: 7, want: 7, wantOK: true},
		{name: "no quote past the end", lines: numbered(10, nil), line: 11, wantOK: false},
		{name: "empty file", lines: nil, line: 1, quote: "x", wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Anchor(c.lines, c.line, c.quote)
			if ok != c.wantOK || (ok && got != c.want) {
				t.Errorf("Anchor = %d, %v; want %d, %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestAnchorHunk(t *testing.T) {
	hunks := []Hunk{
		{Header: "@@ -1,3 +1,4 @@", OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 4},
		{Header: "@@ -88,4 +100,6 @@ func Do", OldStart: 88, OldLines: 4, NewStart: 100, NewLines: 6},
	}
	if h, ok := AnchorHunk(hunks, SideNew, "@@ -88,4 +100,6 @@ func Do", 1); !ok || h.NewStart != 100 {
		t.Errorf("same header = %+v %v", h, ok)
	}
	// The hunk grew: its header changed, and it still holds the note's line.
	if h, ok := AnchorHunk(hunks, SideNew, "@@ -88,4 +100,3 @@ func Do", 103); !ok || h.NewStart != 100 {
		t.Errorf("changed header = %+v %v", h, ok)
	}
	if h, ok := AnchorHunk(hunks, SideOld, "@@ -80,2 +80,2 @@", 90); !ok || h.OldStart != 88 {
		t.Errorf("old side = %+v %v", h, ok)
	}
	if _, ok := AnchorHunk(hunks, SideNew, "@@ -50,1 +50,1 @@", 50); ok {
		t.Error("a hunk that is gone was found")
	}
}

func TestComposeIsDeterministicAndClean(t *testing.T) {
	notes := []Note{
		{ID: "n2", Path: "api/retry.go", Side: SideNew, Line: 100, HunkHeader: "@@ -88,4 +100,6 @@ func Do", Text: "wrap with context", At: 2},
		{ID: "n1", Path: "api/retry.go", Side: SideNew, Line: 42, Quote: "    if err == nil {", Text: "log the attempt number here too", At: 1},
		{ID: "n3", Path: "api/a.go", Side: SideOld, Line: 7, Quote: "old()", Text: "why remove this?\nit was used\x1b[201~ by the CLI", At: 3, Outdated: true},
	}
	want := "Review notes on your changes (vs origin/main), from the person:\n\n" +
		"1. api/a.go:7 (before the change), on \"old()\" (outdated: that line has changed since the note was left)\n" +
		"   why remove this?\n" +
		"   it was used[201~ by the CLI\n" +
		"2. api/retry.go:42, on \"if err == nil {\"\n" +
		"   log the attempt number here too\n" +
		"3. api/retry.go:100-105 (hunk \"@@ -88,4 +100,6 @@\")\n" +
		"   wrap with context\n" +
		"\nAddress each note, then say which you changed."
	for range 3 {
		got := Compose(Message{From: "the person", Base: "origin/main", Notes: notes})
		if got != want {
			t.Fatalf("Compose =\n%s\n\nwant\n%s", got, want)
		}
		notes[0], notes[2] = notes[2], notes[0]
	}
	if strings.ContainsRune(Compose(Message{Notes: notes}), 0x1b) {
		t.Error("an escape reached the message")
	}
}

func TestComposeCutsAndCleansTheQuote(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := Compose(Message{From: "pane build", Notes: []Note{{Path: "a", Side: SideNew, Line: 1, Quote: long + "\x07", Text: "t"}},
		CleanQuote: func(s string) string { return strings.ReplaceAll(s, "x", "y") }})
	if !strings.Contains(got, "a:1, on \""+strings.Repeat("y", QuoteMax)+"...\"") {
		t.Errorf("the quote was not cleaned and cut to %d characters:\n%s", QuoteMax, got)
	}
	if !strings.HasPrefix(got, "Review notes on your changes, from pane build:") {
		t.Errorf("header = %q", strings.SplitN(got, "\n", 2)[0])
	}
}

func TestCleanText(t *testing.T) {
	if got := CleanText("  a\tb \r\n\x1b[31mc\x9b\n\n"); got != "a b\n[31mc" {
		t.Errorf("CleanText = %q", got)
	}
}
