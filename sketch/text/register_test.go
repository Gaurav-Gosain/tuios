package text

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"tuios.dev/sketch"
)

// The tests of this package draw every diagram type, the rare ones too, so
// they add the rare types' drawers before any test runs.
func init() { Rare() }

// TestRareAddsTheRareTypes draws without the rare types' drawers, where a
// Wardley map has no text drawing and the eight types sketch/core parses
// still draw, and then after Rare, where the Wardley map draws.
func TestRareAddsTheRareTypes(t *testing.T) {
	data, err := os.ReadFile("../testdata/corpus/wardley-docs.json")
	if err != nil {
		t.Fatal(err)
	}
	var srcs []string
	if err := json.Unmarshal(data, &srcs); err != nil {
		t.Fatal(err)
	}
	if len(srcs) == 0 {
		t.Fatal("no Wardley source in the corpus")
	}
	parse := func(t *testing.T, src string) *sketch.Diagram {
		t.Helper()
		d, err := sketch.Parse(src, sketch.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	wardley := parse(t, srcs[0])

	saved := more
	t.Cleanup(func() { more = saved })
	more = nil

	if _, err := Draw(wardley, Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a Wardley map without Rare: got %v, want %v", err, ErrUnsupported)
	}
	core := []struct{ name, src string }{
		{"flowchart", "flowchart LR\n  A --> B"},
		{"sequence", "sequenceDiagram\n  Alice->>Bob: hi"},
		{"state", "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Busy : start\n  Busy --> [*]"},
		{"class", "classDiagram\n  class Animal {\n    +name\n    +eat()\n  }\n  Animal <|-- Dog"},
		{"er", "erDiagram\n  CUSTOMER ||--o{ ORDER : places"},
		{"pie", "pie title Pets\n  \"Dogs\" : 3\n  \"Cats\" : 1"},
		{"mindmap", "mindmap\n  root((Root))\n    A\n      B"},
		{"gantt", "gantt\n  dateFormat YYYY-MM-DD\n  section Build\n  One :a, 2024-01-01, 2d"},
	}
	for _, c := range core {
		if _, err := Draw(parse(t, c.src), Options{}); err != nil {
			t.Errorf("%s without Rare: %v", c.name, err)
		}
	}

	Rare()
	if _, err := Draw(wardley, Options{}); err != nil {
		t.Fatalf("a Wardley map after Rare: %v", err)
	}
}
