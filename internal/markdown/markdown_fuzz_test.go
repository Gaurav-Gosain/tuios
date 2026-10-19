package markdown

import (
	"strings"
	"testing"
	"time"
)

// FuzzParse reads arbitrary text as an agent's message. Parse and Inline
// must not panic, every rune a run shows must come from the input (a soft
// break shows as a space), the blocks hold no more than the input (tabs and
// invalid bytes aside), a table holds at most maxTableCells cells, and a text
// at the size bound reads well inside a frame's budget.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"**Navigate & understand**\n- Read `vt/`, `tml/`\n- [x] done\n\n1. one\n   - two\n",
		"# Title\n\n> quote *em* and **strong** and ~~gone~~\n\n```go\nfunc main() {}\n```\n",
		"| a | b |\n|:-|-:|\n| `x|y` | [l](https://e.com \"t\") |\n",
		"see https://tuios.dev/docs, <https://a.b> and <me@x.org>; snake_case_name and 2 * 3 * 4\n",
		"Setext\n===\n\ntext  \nhard\\\nbreak\n\n    indented code\n\n***\n",
		strings.Repeat("*a _b [c `d ", 400),
		strings.Repeat("> - ", 50) + "deep",
		"~~~\nunclosed fence",
		// A wide header and many one-pipe rows: padded, these were
		// millions of cells.
		strings.Repeat("|a", 4000) + "\n" + strings.Repeat("|-", 4000) + "\n" + strings.Repeat("|\n", 8000),
		"|a|b|\n|-|-|\n" + strings.Repeat("|\n", 16000),
		// Small enough to read without the bounds: one past each.
		strings.Repeat("|a", 65) + "|\n" + strings.Repeat("|-", 65) + "|\n|b|\n",
		strings.Repeat("|a", 64) + "|\n" + strings.Repeat("|-", 64) + "|\n" + strings.Repeat("|b|\n", 70),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		start := time.Now()
		blocks, _ := Parse(src)
		var walk func([]Block)
		size := 0
		walk = func(bs []Block) {
			for _, b := range bs {
				if b.Kind == Table && (len(b.Header) > maxTableCols || len(b.Header)*(len(b.Rows)+1) > maxTableCells) {
					t.Fatalf("a table of %d columns and %d rows is past the bounds", len(b.Header), len(b.Rows))
				}
				size += len(b.Text)
				for _, l := range b.Lines {
					size += len(l)
				}
				texts := []string{b.Text}
				texts = append(texts, b.Header...)
				for _, r := range b.Rows {
					texts = append(texts, r...)
				}
				for _, s := range texts[1:] {
					size += len(s)
				}
				for _, s := range texts {
					for _, r := range Inline(s) {
						for _, c := range r.Text {
							if c != ' ' && c != '\n' && !strings.ContainsRune(src, c) && c != '�' {
								t.Fatalf("run %q shows %q, which the input does not hold", r.Text, c)
							}
						}
					}
				}
				walk(b.Children)
			}
		}
		walk(blocks)
		if size > 4*len(src)+64 {
			t.Fatalf("the blocks hold %d bytes of a %d-byte text", size, len(src))
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("reading %d bytes took %v", len(src), d)
		}
	})
}
