//go:build js

package diffview

// Enabled reports whether this build highlights at all. The browser build
// does not: it never shows a review, and chroma's lexers would add megabytes
// to the page.
const Enabled = false

// Highlight draws nothing in the browser build: every line is plain.
func Highlight(string, []string) [][]Span { return nil }
