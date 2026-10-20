package text

import (
	"strings"
	"testing"

	"tuios.dev/sketch"
)

// TestPinnedCore pins one small drawing of each type with a drawing of its
// own here: state, class, ER, pie, mindmap and gantt. A change to the
// layout or to the drawing shows up as a diff a person can read.
func TestPinnedCore(t *testing.T) {
	cases := []struct {
		name, src string
		cols      int
		want      string
	}{
		{"state", "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Busy : start\n  Busy --> [*]", 80, `
     ●
     │
     ▼
 ╭──────╮
 │ Idle │
 ╰──────╯
     │
   start
     │
     ▼
 ╭───────╮
 │ Busy  │
 ╰───────╯
     │
     ▼
     ◉
`},
		{"class", "classDiagram\n  class Animal {\n    +name\n    +eat()\n  }\n  Animal <|-- Dog", 80, `
┌────────┐
│ Animal │
├────────┤
│ +name  │
├────────┤
│ +eat() │
└────────┘
     △
     │
 ┌──────┐
 │ Dog  │
 └──────┘
`},
		{"er", "erDiagram\n  CUSTOMER ||--o{ ORDER : places\n  ORDER {\n    int id PK\n  }", 80, `
 ┌──────────────────┐
 │     CUSTOMER     │
 └──────────────────┘
           │ 1
           │
        places
           │
           │ 0..*
    ┌─────────────┐
    │    ORDER    │
    ├─────────────┤
    │ int  id  PK │
    └─────────────┘
`},
		{"pie", "pie title Pets\n  \"Dogs\" : 3\n  \"Cats\" : 1", 60, `
                         Pets

Dogs  ████████████████████████████████████████  75%  3
Cats  █████████████▍                            25%  1
`},
		{"mindmap", "mindmap\n  root((Root))\n    A\n      B\n    [C]", 80, `
((Root))
├── A
│   └── B
└── [C]
`},
		{"gantt", "gantt\n  dateFormat YYYY-MM-DD\n  axisFormat %m-%d\n  section Build\n  One :done, a, 2024-01-01, 2d\n  Two :crit, b, after a, 2d\n  section Ship\n  Done :milestone, m, after b, 0d", 50, `
Build   ┆         ┆         ┆         ┆         ┆
  One   ░░░░░░░░░░░░░░░░░░░░░         ┆         ┆
  Two   ┆         ┆         ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓
Ship    ┆         ┆         ┆         ┆         ┆
  Done  ┆         ┆         ┆         ┆         ◆
        ├─────────┼─────────┼─────────┼─────────┤
      01-01     01-02     01-03     01-04    01-05
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := sketch.Parse(tc.src, sketch.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			lines, err := Draw(d, Options{Cols: tc.cols})
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(lines, "\n")
			if got != strings.Trim(tc.want, "\n") {
				t.Errorf("got:\n%s\nwant:\n%s", got, strings.Trim(tc.want, "\n"))
			}
		})
	}
}
