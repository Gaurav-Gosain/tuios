package raster

import (
	"testing"
	"time"

	"tuios.dev/sketch"
)

// TestHugeStrokeStaysBounded draws diagrams whose styles put stroke vertices
// about 1e19 pixels away. The work budget used to convert those distances to
// int, where they wrapped around, so the budget passed and the rasterizer
// walked about 2^31 rows for each edge: seconds of CPU for each such node.
// Each drawing must finish fast, on any GOARCH.
func TestHugeStrokeStaysBounded(t *testing.T) {
	styles := []string{
		"stroke-width:1e20",
		"stroke-width:1e19",
		"stroke-width:1e19,stroke-dasharray:1e19 1",
		"stroke-width:4,stroke-dasharray:1e19 1e19,stroke-dashoffset:-1e19",
		"stroke-width:1e20,stroke-linejoin:bevel",
		"stroke-width:1e20,stroke-linejoin:miter",
	}
	for _, style := range styles {
		t.Run(style, func(t *testing.T) {
			src := "flowchart LR\n  A{{one}} --> B([two]) --> C>three] --> D{four}\n  style A " + style +
				"\n  style B " + style + "\n  style C " + style + "\n  style D " + style + "\n"
			svg, err := sketch.Render(src, sketch.RenderOptions{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			start := time.Now()
			if _, err := Draw(svg.Data, Options{Scale: 2, Background: true}); err != nil {
				t.Fatalf("draw: %v", err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("draw took %v, want well under 2s", d)
			}
		})
	}
}
