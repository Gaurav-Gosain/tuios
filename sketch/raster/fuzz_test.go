package raster

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FuzzDraw feeds Draw markup built from the goldens. It must not panic, must
// keep within its pixel limit, and must finish in bounded time.
func FuzzDraw(f *testing.F) {
	files, _ := filepath.Glob("../testdata/golden/flowchart-*.svg")
	for i, name := range files {
		if i%6 != 0 {
			continue
		}
		data, err := os.ReadFile(name)
		if err == nil {
			f.Add(string(data))
		}
	}
	f.Add(`<svg width="40" height="40"><path d="M0,0A1e9,1e9 0 1 1 1e-9,1e-9z" stroke="red" stroke-dasharray="0.0001"/></svg>`)
	f.Fuzz(func(t *testing.T, svg string) {
		if len(svg) > 16384 {
			return
		}
		start := time.Now()
		img, err := Draw(svg, Options{MaxPixels: 4_000_000})
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("draw took %v", d)
		}
		if err == nil && img.Bounds().Dx()*img.Bounds().Dy() > 4_000_000 {
			t.Fatalf("image of %v is past the limit", img.Bounds())
		}
	})
}
