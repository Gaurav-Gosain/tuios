//go:build js

package diffview

// Enabled reports whether this build highlights at all. The browser build
// does not: it never shows a review, and chroma's lexers would add megabytes
// to the page.
const Enabled = false

// Highlight draws nothing in the browser build: every line is plain.
func Highlight(string, []string) [][]Span { return nil }

// Token is a run of one token class in a line. The browser build makes none.
type Token struct {
	Start, End int
	Class      string
}

// Tokens makes no tokens in the browser build.
func Tokens(string, []string, int) [][]Token { return nil }
