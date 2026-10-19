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
		"node label OSC":     "flowchart LR\n  A[\"#27;]2;pwned#7;\"] --> B",
		"edge label C1":      "flowchart LR\n  A -->|\"#155;31m red\"| B",
		"raw ESC":            "flowchart LR\n  A[\"x\x1b]52;c;aGk=\x07y\"] --> B",
		"bidi override":      "flowchart LR\n  A[\"abc#8238;def\"] --> B",
		"bidi marks":         "flowchart LR\n  A[\"a\u200eb\u200fc\u061cd\"] --> B",
		"line separators":    "flowchart LR\n  A[\"a\u2028b\u2029c\"] --> B",
		"sequence message":   "sequenceDiagram\n  A->>B: #27;[2J#127;gone",
		"sequence note C1":   "sequenceDiagram\n  A->>B: hi\n  Note over A: #144;x#156;",
		"sequence actor ESC": "sequenceDiagram\n  participant A as x#27;[31my\n  A->>A: self",
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
