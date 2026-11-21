package text

import (
	"strings"
	"testing"

	sketch "tuios.dev/sketch/internal/engine"
)

// TestFlowchartKeepsItsDirectionWhenItFits draws flowcharts that run across
// (LR), a little wider than the columns there are, at 90 columns.
// Each one fits once the columns that hold only gaps and lines are dropped,
// so it keeps running across: three rows of boxes, or six with a back edge
// routed under them. Turned to run down, each takes thirty rows or more.
//
// A chart that does not fit across even then still turns to run down: six
// long labels at 90 columns.
func TestFlowchartKeepsItsDirectionWhenItFits(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		cols      int
		rows      int
		want      string
	}{
		{"seven in a row", "flowchart LR\n  A[Plan] --> B[Build] --> C[Test] --> D[Review] --> E[Ship] --> F[Watch] --> G[Learn]", 90, 3, "Learn"},
		{"six labels", "flowchart LR\n  A[Read issue] --> B[Write test] --> C[Fix bug] --> D[Run suite] --> E[Open PR] --> F[Merge]", 90, 3, "Merge"},
		{"seven with a back edge", "flowchart LR\n  A[Plan] --> B[Build] --> C[Test] --> D[Review] --> E[Ship] --> F[Watch] --> G[Learn]\n  F -->|regress| C", 90, 6, "regress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := drawSource(t, tc.src, tc.cols)
			text := strings.Join(lines, "\n")
			if len(lines) > tc.rows {
				t.Fatalf("the chart runs down: %d rows, want %d at most\n%s", len(lines), tc.rows, text)
			}
			if w := widest(lines); w > tc.cols {
				t.Fatalf("the chart is %d columns wide, more than %d\n%s", w, tc.cols, text)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("the drawing does not show %q\n%s", tc.want, text)
			}
		})
	}
	t.Run("too wide even squeezed", func(t *testing.T) {
		src := "flowchart LR\n  A[Read the issue] --> B[Write a failing test] --> C[Fix the bug] --> D[Run the suite] --> E[Open a PR] --> F[Merge]"
		lines := drawSource(t, src, 90)
		if len(lines) <= 3 || widest(lines) > 90 {
			t.Fatalf("the chart does not fit across, and it does not run down within 90 columns: %d rows, %d wide\n%s",
				len(lines), widest(lines), strings.Join(lines, "\n"))
		}
	})
}

func drawSource(t *testing.T, src string, cols int) []string {
	t.Helper()
	d, err := sketch.Parse(src, sketch.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lines, err := Draw(d, Options{Cols: cols})
	if err != nil {
		t.Fatal(err)
	}
	return lines
}
