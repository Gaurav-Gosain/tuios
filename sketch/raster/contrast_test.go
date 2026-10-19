package raster

import (
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"tuios.dev/sketch"
)

// Text floors, as WCAG contrast ratios. Every label in every golden, drawn in
// every named theme, is measured against the pixels behind it. Labels in the
// theme's text colour need FloorText, labels in any other theme colour
// (muted text, text on a series colour) FloorOther. Labels whose own colour
// the diagram sets are the author's choice and are skipped.
//
// The goldens that set no colours at all are measured as pele draws them, on
// the theme's grounds (background, surfaces and their blends), so a theme
// text colour that does not read fails. pele also writes its background
// colour on series fills, which pele's own default series fail. Every golden is then measured
// again with Options.Readable, which must lift theme text off any fill the
// diagram sets.
const (
	FloorText  = 4.5
	FloorOther = 3.0
)

// onGround reports whether c is one of the theme's grounds, or a blend of
// the background with one, as a translucent cluster gives.
func onGround(c color.NRGBA, grounds []color.NRGBA) bool {
	d := func(a, b color.NRGBA, t float64) float64 {
		x := func(p, q, v uint8) float64 { return float64(p) + (float64(q)-float64(p))*t - float64(v) }
		return math.Sqrt(x(a.R, b.R, c.R)*x(a.R, b.R, c.R) + x(a.G, b.G, c.G)*x(a.G, b.G, c.G) + x(a.B, b.B, c.B)*x(a.B, b.B, c.B))
	}
	for _, g := range grounds[1:] {
		for t := 0.0; t <= 1; t += 0.05 {
			if d(grounds[0], g, t) < 6 {
				return true
			}
		}
	}
	return false
}

// reAuthored finds a colour a diagram sets itself.
var reAuthored = regexp.MustCompile(`(?i)(fill|stroke|color)\s*[:=]\s*"?\s*(#|rgb|hsl)`)

type finding struct {
	file, text string
	ratio      float64
	floor      float64
}

func TestNamedThemesKeepTextReadable(t *testing.T) {
	if testing.Short() {
		t.Skip("draws every golden in every theme")
	}
	files, _ := filepath.Glob("../testdata/golden/*.svg")
	if len(files) == 0 {
		t.Fatal("no goldens")
	}
	for _, nt := range sketch.Themes() {
		t.Run(nt.Name, func(t *testing.T) {
			th := nt.Theme
			fg := nrgba(th.Fg)
			grounds := []color.NRGBA{nrgba(th.Bg), nrgba(th.Surface), nrgba(th.SurfaceAlt)}
			var bad []finding
			labels := 0
			for _, f := range files {
				for _, readable := range []bool{false, true} {
					data, err := os.ReadFile(f)
					if err != nil {
						t.Fatal(err)
					}
					if !readable && reAuthored.Match(data) {
						continue
					}
					checkContrast(t, string(data), &th, readable, func(c, bg color.NRGBA, authored bool) {
						if authored || (!readable && !onGround(bg, grounds)) {
							return
						}
						labels++
						floor := FloorOther
						if c == fg {
							floor = FloorText
						}
						if r := ratio(c, bg); r < floor {
							name := filepath.Base(f)
							if readable {
								name += " (readable)"
							}
							bad = append(bad, finding{name, hexOf(c) + " on " + hexOf(bg), r, floor})
						}
					})
				}
			}
			sort.Slice(bad, func(i, j int) bool { return bad[i].ratio < bad[j].ratio })
			for i, b := range bad {
				if i == 12 {
					t.Errorf("... and %d more", len(bad)-12)
					break
				}
				t.Errorf("%s: %s is %.2f, floor %.1f", b.file, b.text, b.ratio, b.floor)
			}
			t.Logf("%d labels measured", labels)
		})
	}
}

func nrgba(c color.Color) color.NRGBA {
	return color.NRGBAModel.Convert(c).(color.NRGBA)
}

func hexOf(c color.NRGBA) string {
	const d = "0123456789abcdef"
	return string([]byte{'#', d[c.R>>4], d[c.R&15], d[c.G>>4], d[c.G&15], d[c.B>>4], d[c.B&15]})
}

// checkContrast draws svg in a theme and calls see for each run of text with
// its colour and the colour behind it.
func checkContrast(t *testing.T, svg string, th *sketch.Theme, readable bool, see func(c, bg color.NRGBA, authored bool)) {
	t.Helper()
	if err := loadFaces(); err != nil {
		t.Fatal(err)
	}
	doc, err := parseXML(svg)
	if err != nil {
		t.Fatal(err)
	}
	root := doc
	w, h := lengthOf(attrOr(root, "width"), 300), lengthOf(attrOr(root, "height"), 150)
	if w*h > 4_000_000 {
		return
	}
	dst := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(w)), int(math.Ceil(h))))
	m := identity
	if v := numbers(attrOr(root, "viewBox")); len(v) == 4 && v[2] > 0 && v[3] > 0 {
		m = matrix{w / v[2], 0, 0, h / v[3], 0, 0}.mul(translate(-v[0], -v[1]))
	}
	r, st := newRenderer(root, dst, th, m)
	r.readable = readable
	r.paintBackground()
	r.probe = func(r *renderer, p path, st state) {
		c, ok := st.paintColor(st.fill, st.fillOpacity)
		if !ok || st.opacity < 0.99 {
			return
		}
		b := emptyBox()
		for _, l := range flatten(p, st.m, 1) {
			for _, q := range l.pts {
				b.add(q)
			}
		}
		if b.empty() || strings.TrimSpace(hexOf(c)) == "" {
			return
		}
		bg, ok := backdrop(r.dst, b)
		if !ok {
			return
		}
		see(over(c, bg), bg, st.authored)
	}
	st = r.apply(root, st)
	r.children(root, st)
}
