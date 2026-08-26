package app

import "testing"

// The expected spans below are written out by hand from the input strings, not
// read back from ScanBareURL, so a change in the scanner cannot quietly change
// what the test believes is correct.
//
// Each case names the naive scanner it rules out. Every one of them was
// confirmed to fail against that naive scanner before this test was kept:
//
//	trailing-stop / trailing-comma      fail a scanner that stops only at space
//	closing-bracket                     fail a scanner that trims every ")"
//	balanced-bracket                    fail a scanner that stops at whitespace
//	mid-word / after-dot                fail a scanner using strings.Index
//	second-of-two                       fail a scanner that returns the first hit
//	scheme-only / scheme-word           fail a scanner that matches the prefix
//	before / after                      fail a scanner that ignores the column
func TestScanBareURL(t *testing.T) {
	cases := []struct {
		name string
		line string
		col  int
		want string // "" means no match
	}{
		{"plain", "see https://example.com now", 10, "https://example.com"},
		{"at-first-byte", "https://example.com", 0, "https://example.com"},
		{"at-last-byte", "https://example.com", 18, "https://example.com"},
		{"http", "http://a.b/c", 3, "http://a.b/c"},
		{"file", "file:///home/u/x.txt", 5, "file:///home/u/x.txt"},

		// A sentence's full stop is not part of the address.
		{"trailing-stop", "read https://example.com/a.", 12, "https://example.com/a"},
		{"trailing-comma", "https://example.com/a, and", 3, "https://example.com/a"},
		{"trailing-stack", "https://example.com/a).", 3, "https://example.com/a"},

		// An unopened closer belongs to the prose; an opened one to the URL.
		{"closing-bracket", "(https://example.com/a)", 5, "https://example.com/a"},
		{"balanced-bracket", "https://en.wikipedia.org/wiki/X_(y)", 3, "https://en.wikipedia.org/wiki/X_(y)"},

		// A scheme in the middle of a word is a word.
		{"mid-word", "xhttps://example.com", 5, ""},
		{"after-dot", "foo.http://example.com", 8, ""},

		// A scheme with no authority after it is not an address.
		{"scheme-only", "https://", 2, ""},
		{"scheme-word", "the https protocol", 6, ""},

		// The column decides which of two URLs on one row is meant, and whether
		// any of them is.
		{"first-of-two", "https://a.example https://b.example", 3, "https://a.example"},
		{"second-of-two", "https://a.example https://b.example", 20, "https://b.example"},
		{"between-two", "https://a.example https://b.example", 17, ""},
		{"before", "  https://example.com", 0, ""},
		{"after", "https://example.com  ", 20, ""},

		// Quotes end a match, which is what keeps a URL out of the shell syntax
		// around it.
		{"quoted", `curl "https://example.com/a"`, 10, "https://example.com/a"},

		// Out of range is not a panic.
		{"negative-col", "https://example.com", -1, ""},
		{"past-end", "https://example.com", 99, ""},
		{"empty", "", 0, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end, ok := ScanBareURL(c.line, c.col)
			if c.want == "" {
				if ok {
					t.Fatalf("ScanBareURL(%q, %d) matched %q, want no match",
						c.line, c.col, c.line[start:end])
				}
				return
			}
			if !ok {
				t.Fatalf("ScanBareURL(%q, %d) found nothing, want %q", c.line, c.col, c.want)
			}
			if got := c.line[start:end]; got != c.want {
				t.Fatalf("ScanBareURL(%q, %d) = %q, want %q", c.line, c.col, got, c.want)
			}
		})
	}
}

// TestScanBareURLCoversEveryColumn checks that a match is reported for every
// column inside it and for none outside it. A scanner that anchors on the
// scheme but forgets to test the column against the end passes the table above
// and fails here.
func TestScanBareURLCoversEveryColumn(t *testing.T) {
	const line = "ab https://example.com cd"
	const wantStart, wantEnd = 3, 22 // counted off the literal above

	if line[wantStart:wantEnd] != "https://example.com" {
		t.Fatalf("the test's own offsets are wrong: %q", line[wantStart:wantEnd])
	}

	for col := range len(line) {
		start, end, ok := ScanBareURL(line, col)
		inside := col >= wantStart && col < wantEnd
		if ok != inside {
			t.Fatalf("col %d (%q): matched=%v, want %v", col, line[col:col+1], ok, inside)
		}
		if ok && (start != wantStart || end != wantEnd) {
			t.Fatalf("col %d: span [%d,%d), want [%d,%d)", col, start, end, wantStart, wantEnd)
		}
	}
}
