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
		"journey task":           "journey\n  title t#27;]0x#7;\n  section s#155;31m\n  #27;[2J: 5: a#144;b, c#27;[31m",
		"kanban card":            "kanban\n  t#27;]0x#7;\n    a[#27;[2J#155;31m]@{ ticket: \"#27;[31m\", assigned: \"#144;x\", priority: \"#127;\" }",
		"git commit":             "gitGraph\n  title t#27;]0x#7;\n  commit id: \"a#27;[2J\" tag: \"#155;31m\"\n  branch \"b#144;x\"\n  commit",
		"sankey title":           "---\ntitle: \"t\\e]2;x\\a\\x9b31m\"\n---\nsankey\n\na,b,3\nb,c,2\n",
		"block":                  "block\n  columns 2\n  a[\"#27;[2J#155;31m\"] b<[\"#144;x\"]>(right)\n  block:g[\"t#27;]0x#7;\"]\n    c\n  end\n  a -- \"#27;[31m\" --> c",
		"architecture":           "architecture-beta\n  group g(cloud)[\"t#27;]0x#7;\"]\n  service a \"#155;31m\"[\"a#27;[2J\"] in g\n  service b(server)\n  a:R -[#144;x]-> L:b",
		"requirement":            "requirementDiagram\n  requirement \"r#27;[2J\" {\n  id: 1\n  text: \"t#155;31m\"\n  }\n  element e {\n  type: \"#144;x\"\n  }\n  e - satisfies -> \"r#27;[2J\"",
		"c4":                     "C4Context\n  title t#27;]0x#7;\n  Person(u, \"u#27;[2J\", \"#155;31m\")\n  System_Boundary(b, \"#144;x\") {\n  System(s, \"s\", \"#27;[31m\")\n  }\n  Rel(u, s, \"#27;[2J\", \"#155;31m\")",
		"packet":                 "packet\n  title t#27;]0x#7;\n  0-15: \"a#27;[2J\"\n  16-31: \"#155;31m#144;x\"",
		"radar":                  "radar-beta\n  title t#27;]0x#7;\n  axis a[\"#27;[2J\"], b[\"#144;x\"]\n  curve c[\"#155;31m\"]{1, 2}",
		"treemap":                "treemap-beta\n  title t#27;]0x#7;\n  \"#27;[2J\"\n    \"#155;31m\": 10\n    \"#144;x\": 5",
		"treemap tree":           "treemap-beta\n  \"s#27;[2J\"\n    \"a very long name #155;31m that cannot fit in any box at all\": 10\n    \"#144;x\": 1",
		"info":                   "info\n  title t#27;]0x#7;#155;31m",
		"treeView":               "treeView-beta\n  title t#27;]0x#7;\n  a#27;[2J/ ## #155;31m\n    b#144;x :::highlight",
		"ishikawa":               "ishikawa-beta\n  e#27;]0x#7;\n    c#155;31m\n      a#144;x",
		"ishikawa tree":          "ishikawa-beta\n  e#27;]0x#7;\n    c#155;31m\n      a very long cause #144;x that will never fit in the columns there are at all",
		"cynefin":                "cynefin-beta\n  title t#27;]0x#7;\n  complex\n    \"#27;[2J\"\n  confusion\n    \"#155;31m\"\n  clear --> chaotic : \"#144;x\"",
		"eventmodeling":          "eventmodeling\ntf 01 ui A \"#27;]0x#7;\"\ntf 02 cmd B { #155;31m }\nnote 02 {\n  #144;x\n}\n",
		"venn":                   "venn-beta\n  title t#27;]0x#7;\n  set A[\"#27;[2J\"]\n    text q[\"#144;x\"]\n  set B\n  union A, B[\"#155;31m\"]",
		"wardley":                "wardley-beta\ntitle t#27;]0x#7;\ncomponent \"#27;[2J\" [0.5, 0.5]\ncomponent B [0.2, 0.2]\n\"#27;[2J\" -> B; #155;31m\nnote \"#144;x\" [0.3, 0.3]",
		"railroad":               "railroad-beta\ntitle t#27;]0x#7;\nr = sequence(terminal(\"#27;[2J\"), nonterminal(\"#155;31m\"), special(\"#144;x\")) ;",
		"railroad text":          "railroad-ebnf-beta\nr = \"a very long terminal #27;[2J that cannot fit in any narrow column at all, not even close\" ;",
		"swimlane":               "swimlane-beta LR\n  subgraph L[\"t#27;]0x#7;\"]\n    a[\"#27;[2J#155;31m\"] -- \"#27;[31m\" --> b\n  end\n  subgraph M[\"#144;x\"]\n    c\n  end\n  b --> c",
		"usecase":                "usecase-beta\nactor \"a#27;[2J\" <<#155;31m>>\nsystemBoundary b[\"t#27;]0x#7;\"]\n  u(\"#144;x\")\nend\njson j@{\"#27;[31m\": \"#27;[2J\"}\na_27__2J -- \"#27;[31m\" --> u\nnote for u \"#27;[2J\"",
		"agentflow":              "agentflow-beta TB\n  flow f[\"t#27;]0x#7;\"]\n    a[\"#27;[2J#155;31m\"]@{ shape: tool }\n  end\n  b[\"#144;x\"]\n  a -- \"#27;[31m\" --> b",
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
