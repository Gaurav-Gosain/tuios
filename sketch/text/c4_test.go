package text

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"tuios.dev/sketch"
)

// drawPinned parses src, draws it at cols, and compares the lines with want.
func drawPinned(t *testing.T, src string, cols int, want string) {
	t.Helper()
	d, err := sketch.Parse(src, sketch.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lines, err := Draw(d, Options{Cols: cols})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "\n"); got != strings.Trim(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// fitsCorpus draws every corpus diagram of a type at cols and wants each to fit.
func fitsCorpus(t *testing.T, name string, cols int) {
	t.Helper()
	data, err := os.ReadFile("../testdata/corpus/" + name + "-docs.json")
	if err != nil {
		t.Fatal(err)
	}
	var srcs []string
	if err := json.Unmarshal(data, &srcs); err != nil {
		t.Fatal(err)
	}
	for i, src := range srcs {
		d, err := sketch.Parse(src, sketch.ParseOptions{})
		if err != nil {
			t.Fatalf("#%d: %v", i, err)
		}
		lines, err := Draw(d, Options{Cols: cols})
		if err != nil {
			t.Fatalf("#%d at %d columns: %v", i, cols, err)
		}
		for _, line := range lines {
			if w := width(line); w > cols {
				t.Fatalf("#%d: a line of %d columns at %d: %q", i, w, cols, line)
			}
		}
	}
}

func TestC4Text(t *testing.T) {
	drawPinned(t, "C4Context\nPerson(u, \"User\")\nSystem_Boundary(b, \"Shop\") {\nSystem(s, \"Store\", \"Sells\")\n}\nRel(u, s, \"Buys\")", 0, `
 ╭──────────────────────╮
 │       «person»       │
 │         User         │
 ╰──────────────────────╯
              │
 ┌─ Shop ─────┼───────────────┐
 │          Buys              │
 │            │               │
 │            ▼               │
 │  ┌──────────────────────┐  │
 │  │       «system»       │  │
 │  │        Store         │  │
 │  │        Sells         │  │
 │  └──────────────────────┘  │
 └────────────────────────────┘
`)
	fitsCorpus(t, "c4", 80)
}
