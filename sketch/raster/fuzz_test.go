package raster

import (
	"image"
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

// FuzzMotion moves between two drawings built from the goldens: a golden and
// the fuzzed markup. Parsing, matching and drawing frames must not panic and
// must finish in bounded time.
func FuzzMotion(f *testing.F) {
	files, _ := filepath.Glob("../testdata/golden/*.svg")
	var seeds []string
	for i, name := range files {
		if i%40 != 0 {
			continue
		}
		if data, err := os.ReadFile(name); err == nil {
			seeds = append(seeds, string(data))
			f.Add(string(data), 0.3)
		}
	}
	f.Add(`<svg width="40" height="40"><g data-id="a" class="pele-edge"><path d="M0,0"/><path class="pele-marker" d="M0,0Z"/></g></svg>`, 0.5)
	f.Fuzz(func(t *testing.T, svg string, p float64) {
		if len(svg) > 16384 || len(seeds) == 0 {
			return
		}
		from, err := ParseScene(seeds[len(svg)%len(seeds)])
		if err != nil {
			t.Fatal(err)
		}
		to, err := ParseScene(svg)
		if err != nil {
			return
		}
		start := time.Now()
		m := NewMotion(NewMotion(nil, 1, from), 1, to)
		m = NewMotion(m, p, from)
		dst := image.NewRGBA(image.Rect(0, 0, 320, 200))
		for _, q := range []float64{0, p, 0.5, 1} {
			_ = m.Draw(dst, q, FrameOptions{Readable: true})
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("motion took %v", d)
		}
	})
}
