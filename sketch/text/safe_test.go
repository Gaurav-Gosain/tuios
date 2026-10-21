package text

import (
	"regexp"
	"strings"
	"testing"

	"tuios.dev/sketch"
)

// TestNoTerminalControls draws labels that decode to terminal controls and
// checks that none of them reach the output. Labels are entity-decoded, so
// "#27;" is ESC and "#155;" is CSI. Without safeRune the first case writes
// OSC 2 (the window title) to the terminal that shows the drawing.
func TestNoTerminalControls(t *testing.T) {
	sources := map[string]string{
		"node label OSC":         "flowchart LR\n  A[\"#27;]2;pwned#7;\"] --> B",
		"edge label C1":          "flowchart LR\n  A -->|\"#155;31m red\"| B",
		"raw ESC":                "flowchart LR\n  A[\"x\x1b]52;c;aGk=\x07y\"] --> B",
		"bidi override":          "flowchart LR\n  A[\"abc#8238;def\"] --> B",
		"bidi marks":             "flowchart LR\n  A[\"a\u200eb\u200fc\u061cd\"] --> B",
		"line separators":        "flowchart LR\n  A[\"a\u2028b\u2029c\"] --> B",
		"sequence message":       "sequenceDiagram\n  A->>B: #27;[2J#127;gone",
		"sequence note C1":       "sequenceDiagram\n  A->>B: hi\n  Note over A: #144;x#156;",
		"sequence actor ESC":     "sequenceDiagram\n  participant A as x#27;[31my\n  A->>A: self",
		"state label OSC":        "stateDiagram-v2\n  s1 : x#27;]2;pwned#7;\n  [*] --> s1",
		"state edge C1":          "stateDiagram-v2\n  s1 --> s2 : #155;31m red",
		"class member ESC":       "classDiagram\n  class A {\n    +x#27;[31m\n  }",
		"class card C1":          "classDiagram\n  A \"#155;31m\" --> \"1\" B",
		"er comment OSC":         "erDiagram\n  A {\n    string name \"#27;]52;c;eA==#7;\"\n  }",
		"er label ESC":           "erDiagram\n  A ||--o{ B : \"x#27;[2Jy\"",
		"pie label ESC":          "pie title t#27;]2;x#7;\n  \"a#27;[2J\" : 1",
		"mindmap node OSC":       "mindmap\n  r(\"a#27;]2;b#7;\")\n    child #155;31m x",
		"gantt task ESC":         "gantt\n  dateFormat YYYY-MM-DD\n  section S#27;[31m\n  t#27;]2;x#7; :a1, 2024-01-01, 3d",
		"state description bidi": "stateDiagram-v2\n  s1 : line one\n  s1 : a#8238;b\n  [*] --> s1",
		"class annotation OSC":   "classDiagram\n  class A {\n    <<x#27;]2;t#7;>>\n    +f()\n  }",
		"class member bidi":      "classDiagram\n  class A {\n    +a#8238;b\n  }",
		"er type C0":             "erDiagram\n  A {\n    list~x#27;y~ name\n  }",
		"er type bidi":           "erDiagram\n  A {\n    a\u202eb name\n  }",
		"er comment bidi":        "erDiagram\n  A {\n    string name \"a#8238;b\"\n  }",
		"pie label bidi":         "pie\n  \"a#8238;b\" : 1",
		"mindmap node bidi":      "mindmap\n  root\n    a#8238;b",
		"gantt vert label OSC":   "gantt\n  dateFormat YYYY-MM-DD\n  section S\n  t :a, 2024-01-01, 3d\n  v#27;]2;x#7; :vert, v1, 2024-01-02, 0d",
		"gantt axisFormat bidi":  "gantt\n  dateFormat YYYY-MM-DD\n  axisFormat %Y\u202e%m\n  section S\n  t :a, 2024-01-01, 3d",
		"gantt section bidi":     "gantt\n  dateFormat YYYY-MM-DD\n  section a#8238;b\n  t :a, 2024-01-01, 3d",
		"timeline period":        "timeline\n  title t#27;]2;x#7;\n  section s#155;31m\n  #27;[2J : e\n  2002 : a#144;b",
		"xychart labels":         "xychart-beta\n  title \"t#27;]2;x#7;\"\n  x-axis \"x#155;31m\" [\"a#27;[2J\", b]\n  y-axis \"y#144;\"\n  bar \"s#127;\" [1, 2]\n  line \"l#27;c\" [2, 1]",
		"quadrant labels":        "quadrantChart\n  title \"t#27;]2;x#7;\"\n  x-axis \"l#155;31m\" --> \"h#27;[2J\"\n  y-axis \"b#144;\" --> \"t#127;\"\n  quadrant-1 \"q#27;c\"\n  \"p#27;]52;c;x#7;\": [0.3, 0.6]",
		"xychart point label":    "xychart-beta\n  x-axis [a, b, c]\n  y-axis 0 --> 4\n  line [2 \"p#27;]2;x#7;\", 2, 2]",
		"xychart across label":   "xychart-beta horizontal\n  x-axis [a, b]\n  line [1 \"p#155;31m\", 2]",
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			d, err := sketch.Parse(src, sketch.ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			for _, color := range []bool{false, true} {
				lines, err := Draw(d, Options{Cols: 200, Color: color})
				if err != nil {
					t.Fatalf("draw: %v", err)
				}
				out := strings.Join(lines, "\n")
				if color {
					// The only escapes are the SGR colours Draw writes itself.
					out = reSGR.ReplaceAllString(out, "")
				}
				for _, r := range out {
					if r != '\n' && safeRune(r) != r {
						t.Fatalf("output holds %U:\n%q", r, out)
					}
				}
				if !strings.ContainsRune(out, '\uFFFD') {
					t.Fatalf("the control was dropped instead of shown as U+FFFD:\n%s", out)
				}
			}
		})
	}
}

// TestCellCap draws with no column bound a diagram whose canvas would pass
// MaxCells, and wants ErrTooLarge instead of a large allocation.
func TestCellCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	// A wide rank: 400 nodes under one root, with long labels.
	for i := range 400 {
		b.WriteString("  R --> W" + itoa(i) + "[\"" + strings.Repeat("w", 40) + "\"]\n")
	}
	// A deep chain below it.
	for i := range 1500 {
		b.WriteString("  C" + itoa(i) + " --> C" + itoa(i+1) + "\n")
	}
	b.WriteString("  W0 --> C0\n")
	d, err := sketch.Parse(b.String(), sketch.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Draw(d, Options{}); err != ErrTooLarge {
		t.Fatalf("Draw with no column bound: %v, want ErrTooLarge", err)
	}
}

var reSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}
