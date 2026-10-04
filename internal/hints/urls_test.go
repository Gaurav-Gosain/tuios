package hints

import (
	"slices"
	"strings"
	"testing"
)

// The ways a URL detector goes wrong, written down before the detector:
//
//   - It runs past the end of a URL into the markup around it: a markdown
//     image inside a link is one match from the first scheme to the last
//     bracket but one.
//   - It cuts a URL that holds a balanced bracket pair (Wikipedia).
//   - It keeps the sentence's punctuation, or a closing bracket the sentence
//     opened.
//   - It stops late at a quote, so an HTML attribute value takes the rest of
//     the tag with it.
//   - It finds a scheme in the middle of a word, or a bare scheme with
//     nothing after it.
//   - It misses the second of two URLs on one line, or the column test is
//     wrong at either end of a match.
//
// The expected texts are written out by hand from the inputs.
func TestURLs(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"plain", "see https://example.com now", []string{"https://example.com"}},
		{"markdown badge", "[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/B0B81N8V1R)",
			[]string{"https://ko-fi.com/img/githubbutton_sm.svg", "https://ko-fi.com/B0B81N8V1R"}},
		{"markdown chart", "[![Star History Chart](https://api.star-history.com/svg?repos=a/b&type=Date)](https://star-history.com/#a/b&Date)",
			[]string{"https://api.star-history.com/svg?repos=a/b&type=Date", "https://star-history.com/#a/b&Date"}},
		{"markdown link", "see [the docs](https://tuios.dev/docs).", []string{"https://tuios.dev/docs"}},
		{"wikipedia parens", "https://en.wikipedia.org/wiki/Go_(programming_language)",
			[]string{"https://en.wikipedia.org/wiki/Go_(programming_language)"}},
		{"wikipedia parens in markdown", "[Go](https://en.wikipedia.org/wiki/Go_(language))",
			[]string{"https://en.wikipedia.org/wiki/Go_(language)"}},
		{"square brackets in a query", "https://x.org/?a[0]=1 ok", []string{"https://x.org/?a[0]=1"}},
		{"closing bracket of the sentence", "(see https://example.com/a).", []string{"https://example.com/a"}},
		{"trailing punctuation", "go to https://example.com/x, then https://example.com/y!", []string{"https://example.com/x", "https://example.com/y"}},
		{"trailing colon and question", "is it https://example.com/z?", []string{"https://example.com/z"}},
		{"angle autolink", "<https://example.com/auto>", []string{"https://example.com/auto"}},
		{"html attribute", `<img src="https://example.com/a.png" alt='x'>`, []string{"https://example.com/a.png"}},
		{"single quoted", `curl 'https://example.com/q?a=1'`, []string{"https://example.com/q?a=1"}},
		{"backtick", "run `https://example.com/b` now", []string{"https://example.com/b"}},
		{"mid word", "xhttps://example.com", nil},
		{"after dot", "foo.http://example.com", nil},
		{"scheme only", "https:// alone", nil},
		{"scheme then punctuation", "https://.", nil},
		{"file", "file:///home/u/x.txt", []string{"file:///home/u/x.txt"}},
		{"ssh and ftp", "ssh://host/repo ftp://h/f", []string{"ssh://host/repo", "ftp://h/f"}},
		{"non-ascii path", "https://example.com/café/文档", []string{"https://example.com/café/文档"}},
		{"html closing tag only", "</p>", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, r := range URLs(c.line) {
				got = append(got, c.line[r[0]:r[1]])
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("URLs(%q)\n got %q\nwant %q", c.line, got, c.want)
			}
		})
	}
}

// TestURLAtCoversEveryByte checks the column test at both ends of a match and
// between two matches.
func TestURLAtCoversEveryByte(t *testing.T) {
	const line = "ab https://a.example cd https://b.example"
	spans := [][2]int{{3, 20}, {24, 41}} // counted off the literal above
	for _, sp := range spans {
		if !strings.HasPrefix(line[sp[0]:sp[1]], "https://") {
			t.Fatalf("the test's own offsets are wrong: %q", line[sp[0]:sp[1]])
		}
	}
	for i := range len(line) {
		s, e, ok := URLAt(line, i)
		inside := -1
		for k, sp := range spans {
			if i >= sp[0] && i < sp[1] {
				inside = k
			}
		}
		if ok != (inside >= 0) {
			t.Fatalf("byte %d: matched=%v, want %v", i, ok, inside >= 0)
		}
		if ok && (s != spans[inside][0] || e != spans[inside][1]) {
			t.Fatalf("byte %d: span [%d,%d), want %v", i, s, e, spans[inside])
		}
	}
	for _, i := range []int{-1, len(line), 99} {
		if _, _, ok := URLAt(line, i); ok {
			t.Fatalf("byte %d is out of range and matched", i)
		}
	}
}

// FuzzURLs holds the detector to the properties every caller relies on:
// matches are in order and do not overlap, each starts with a scheme and has
// more after it, none holds a byte that ends a URL, none ends in trailing
// punctuation, and no closing bracket in a match is unopened.
func FuzzURLs(f *testing.F) {
	for _, s := range []string{
		"[![a](https://x.org/b.svg)](https://x.org/c)",
		"https://en.wikipedia.org/wiki/Go_(x)).",
		`<a href="http://x/y">z</a> file:///tmp/a`,
		"xhttps://a https:// http://a.b/c?d=[1]",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		prev := 0
		for _, r := range URLs(line) {
			s, e := r[0], r[1]
			if s < prev || e <= s || e > len(line) {
				t.Fatalf("bad span %v after %d in %q", r, prev, line)
			}
			prev = e
			text := line[s:e]
			n := urlSchemeAt(line, s)
			if n == 0 || len(text) <= n {
				t.Fatalf("%q does not start with a scheme and more", text)
			}
			if strings.IndexByte(urlTrailing, text[len(text)-1]) >= 0 {
				t.Fatalf("%q keeps trailing punctuation", text)
			}
			parens, squares := 0, 0
			for i := range len(text) {
				c := text[i]
				if urlStop(c) {
					t.Fatalf("%q holds stop byte %q", text, c)
				}
				switch c {
				case '(':
					parens++
				case ')':
					parens--
				case '[':
					squares++
				case ']':
					squares--
				}
				if parens < 0 || squares < 0 {
					t.Fatalf("%q closes a bracket it never opened", text)
				}
			}
		}
	})
}
