//go:build differential

package raster

// Draws every flowchart golden with sketch/raster and with rsvg-convert, an
// independent renderer, and compares the two pictures. Needs rsvg-convert and
// the full Arimo fonts in SKETCH_ARIMO_DIR (rsvg finds them through fontconfig).
//
//	SKETCH_ARIMO_DIR=~/fonts/arimo go test -tags differential -run TestDifferential -v ./raster/
//
// SKETCH_DIFF_OUT keeps the pictures of the worst cases in that folder.

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The share of pixels that may differ by more than 24/255 in a channel, after
// both pictures are blurred by a pixel so that antialiasing and font hinting do
// not count. Correct output measures 0; leaving out the dashes of dotted edges
// measures 0.2% and more.
const maxFaint = 0.0005

var (
	reRootStyle = regexp.MustCompile(`<svg[^>]*style="([^"]*)"`)
	reVarDecl   = regexp.MustCompile(`(--[\w-]+):((?:[^;()]|\([^()]*(?:\([^()]*\))?[^()]*\))*)`)
	reVarUse    = regexp.MustCompile(`var\((--[\w-]+)(?:,([^()]*))?\)`)
	reStyleRx   = regexp.MustCompile(`style="rx:([\d.]+)px"`)
	reStyleRxIn = regexp.MustCompile(`style="rx:([\d.]+)px;`)
)

// resolveForRsvg writes the variables out, as resolve.py does: rsvg does not read var().
// It is written apart from css.go, so that a fault there does not hide in both pictures.
func resolveForRsvg(svg string) string {
	env := map[string]string{"--pele-font": "Arimo", "--pele-font-mono": "Go Mono"}
	ev := func(v string) string {
		for range 6 {
			v = reVarUse.ReplaceAllStringFunc(v, func(m string) string {
				g := reVarUse.FindStringSubmatch(m)
				if x, ok := env[g[1]]; ok {
					return x
				}
				if g[2] != "" {
					return g[2]
				}
				return "black"
			})
		}
		return v
	}
	if m := reRootStyle.FindStringSubmatch(svg); m != nil {
		for _, d := range reVarDecl.FindAllStringSubmatch(m[1], -1) {
			if _, set := env[d[1]]; !set {
				env[d[1]] = ev(d[2])
			}
		}
	}
	out := ev(svg)
	out = reStyleRx.ReplaceAllString(out, `rx="$1"`)
	return reStyleRxIn.ReplaceAllString(out, `rx="$1" style="`)
}

func blur(img image.Image) ([]float64, int, int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src := make([]float64, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// Over white.
			wa := float64(0xffff - a)
			i := (y*w + x) * 3
			src[i], src[i+1], src[i+2] = (float64(r)+wa)/257, (float64(g)+wa)/257, (float64(bl)+wa)/257
		}
	}
	out := make([]float64, len(src))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				sum, n := 0.0, 0.0
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						xx, yy := x+dx, y+dy
						if xx >= 0 && yy >= 0 && xx < w && yy < h {
							sum += src[(yy*w+xx)*3+c]
							n++
						}
					}
				}
				out[(y*w+x)*3+c] = sum / n
			}
		}
	}
	return out, w, h
}

// difference is the share of pixels that differ by more than a quarter of the
// range in some channel, the share that differ by more than a tenth, the mean
// difference, and a picture of where.
func difference(a, b image.Image) (float64, float64, float64, *image.RGBA) {
	pa, w, h := blur(a)
	pb, w2, h2 := blur(b)
	if w != w2 || h != h2 {
		return 1, 1, 255, nil
	}
	faint := 0
	mean := 0.0
	heat := image.NewRGBA(image.Rect(0, 0, w, h))
	count := 0
	for i := 0; i < w*h; i++ {
		d := 0.0
		for c := 0; c < 3; c++ {
			d = math.Max(d, math.Abs(pa[i*3+c]-pb[i*3+c]))
		}
		mean += d
		if d > 24 {
			faint++
		}
		g := uint8(255 - math.Min(255, d*3))
		px := heat.Pix[i*4 : i*4+4]
		px[0], px[1], px[2], px[3] = 255, g, g, 255
		if d > 64 {
			count++
			px[0], px[1], px[2] = 200, 0, 0
		}
	}
	n := float64(w * h)
	return float64(count) / n, float64(faint) / n, mean / n, heat
}

// covered reports whether the embedded faces have a glyph for every character of the SVG's text.
func covered(svg string) bool {
	if err := loadFaces(); err != nil {
		return false
	}
	root, err := parseXML(svg)
	if err != nil {
		return false
	}
	ok := true
	var walk func(n *node, inText bool)
	walk = func(n *node, inText bool) {
		if n.name == "" && inText {
			for _, r := range n.text {
				if r < 32 || r == ' ' {
					continue
				}
				if _, g := (state{}).glyphOf(r); g == nil {
					ok = false
				}
			}
		}
		for _, c := range n.children {
			walk(c, inText || n.name == "text")
		}
	}
	walk(root, false)
	return ok
}

func TestDifferential(t *testing.T) {
	arimo := os.Getenv("SKETCH_ARIMO_DIR")
	if arimo == "" {
		t.Skip("set SKETCH_ARIMO_DIR to a folder with Arimo-Regular.ttf and Arimo-Bold.ttf")
	}
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		t.Skip("needs rsvg-convert")
	}
	fontDir := t.TempDir()
	gz, _ := fontFiles.ReadFile("fonts/Go-Mono.ttf.gz")
	zr, _ := gzip.NewReader(bytes.NewReader(gz))
	mono, _ := io.ReadAll(zr)
	os.WriteFile(filepath.Join(fontDir, "Go-Mono.ttf"), mono, 0o644)
	conf := filepath.Join(fontDir, "fonts.conf")
	os.WriteFile(conf, []byte(`<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd"><fontconfig>`+
		`<include ignore_missing="yes">/etc/fonts/fonts.conf</include><dir>`+arimo+`</dir><dir>`+fontDir+`</dir>`+
		`<cachedir>`+filepath.Join(fontDir, "cache")+`</cachedir></fontconfig>`), 0o644)
	outDir := os.Getenv("SKETCH_DIFF_OUT")

	files, _ := filepath.Glob("../testdata/golden/flowchart-*.svg")
	sort.Strings(files)
	type result struct {
		name    string
		differ  float64
		faint   float64
		mean    float64
		covered bool
		ours    *image.RGBA
		theirs  image.Image
		heatmap *image.RGBA
	}
	var results []result
	for _, f := range files {
		data, _ := os.ReadFile(f)
		ours, err := Draw(string(data), Options{Scale: 2, Background: true})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		cmd := exec.Command("rsvg-convert", "-z", "2", "-b", "white")
		cmd.Env = append(os.Environ(), "FONTCONFIG_FILE="+conf)
		cmd.Stdin = strings.NewReader(resolveForRsvg(string(data)))
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: rsvg-convert: %v", f, err)
		}
		theirs, err := png.Decode(&out)
		if err != nil {
			t.Fatal(err)
		}
		d, faint, mean, heat := difference(ours, theirs)
		results = append(results, result{strings.TrimSuffix(filepath.Base(f), ".svg"), d, faint, mean, covered(string(data)), ours, theirs, heat})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].mean > results[j].mean })
	sum := 0.0
	for _, r := range results {
		sum += r.differ
		if !r.covered {
			// rsvg draws such characters from a system font; sketch leaves a box of the measured width.
			t.Logf("%s: has characters the embedded fonts lack; %.2f%% of pixels differ", r.name, 100*r.differ)
			continue
		}
		if r.faint > maxFaint {
			t.Errorf("%s: %.3f%% of pixels differ, more than %.2f%%", r.name, 100*r.faint, 100*maxFaint)
		}
	}
	var report strings.Builder
	fmt.Fprintf(&report, "%d diagrams, mean %.3f%% of pixels differ; worst:\n", len(results), 100*sum/float64(len(results)))
	for i, r := range results[:min(5, len(results))] {
		fmt.Fprintf(&report, "  %d. %s: mean difference %.2f/255, %.3f%% of pixels off by more than 24/255, %.3f%% by more than 64/255\n", i+1, r.name, r.mean, 100*r.faint, 100*r.differ)
		if outDir != "" {
			save := func(suffix string, img image.Image) {
				if img == nil {
					return
				}
				f, err := os.Create(filepath.Join(outDir, r.name+suffix))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
			save("-sketch.png", r.ours)
			save("-rsvg.png", r.theirs)
			save("-diff.png", r.heatmap)
		}
	}
	t.Log(report.String())
}
