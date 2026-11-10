package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// TestTMLChartsShowWhatLoomShows draws the charts and media of TML part 4
// in guios and in loom from the same document. The ways guios can get a
// chart wrong, each checked here:
//
//   - it reads a row's value from another field, or none, so a bar's height
//     is not its value: the bars' heights must keep the values' ratio;
//   - it folds, colours or rounds a pie's slices another way than loom, so
//     the legend's shares differ: each share loom writes must show;
//   - it names the series of a line, the countries' shades of a map, a
//     gauge's share or a sparkline's last value otherwise than loom;
//   - it shows a data: image as its alt text, or a file: image (which is on
//     the daemon's machine) as a broken picture;
//   - it shows a diagram that cannot be drawn as nothing at all.
func TestTMLChartsShowWhatLoomShows(t *testing.T) {
	pic := testPNG(t, color.NRGBA{R: 250, G: 20, B: 200, A: 255})
	doc := checkTML(t, `<tml version="1">
  <state>{"langs": [{"l": "go", "n": 3}, {"l": "rust", "n": 1}],
          "runs": [{"d": "mon", "pass": 5, "fail": 1}, {"d": "tue", "pass": 7, "fail": 2}],
          "where": [{"c": "US", "v": 10}, {"c": "FR", "v": 2}]}</state>
  <col gap="1">
    <pie data="state.langs" label="l" v="n" title="Languages"/>
    <line data="state.runs" x="d" y="pass,fail" title="Runs"/>
    <map data="state.where" x="c" y="v" title="Where"/>
    <gauge value="30" max="60" label="disk"/>
    <sparkline values="[1, 4, 2, 9]" title="load"/>
    <image src="`+pic+`" alt="inline picture"/>
    <image src="file:shots/a.png" alt="the shot"/>
    <mermaid>this is not a diagram</mermaid>
  </col>
</tml>`)
	s, err := loom.New(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// loom pads the legend into columns, such as "go   75%".
	cells := strings.Join(strings.Fields(s.Render(100, 80).String()), " ")
	a, _ := tmlApp(t, doc)
	a.tmlWork = func(work func() func()) { work()() }
	tester := ui.NewTester(func(c *ui.Context) {
		a.paneTML(c, paletteFor(c), &proto.Pane{Surface: "s1"})
	}, 900, 1400)
	for range 3 {
		tester.Frame()
	}
	texts := strings.Join(tester.Texts(), " | ")
	for _, w := range []string{"go 75%", "rust 25%", "pass", "fail", "disk", "50%", "load", "9", "the shot"} {
		if !strings.Contains(cells, w) {
			t.Fatalf("the test is wrong: loom does not show %q:\n%s", w, cells)
		}
		if !strings.Contains(texts, w) {
			t.Errorf("ASSERTION: loom shows %q, guios does not.\nguios: %s", w, texts)
		}
	}
	// The map's legend: the shades loom names, from the lowest to the
	// highest.
	for _, w := range []string{"≥ 2", "≥ 8"} {
		if !strings.Contains(cells, w) {
			t.Fatalf("the test is wrong: loom's map legend does not show %q:\n%s", w, cells)
		}
		if !strings.Contains(texts, w) {
			t.Errorf("ASSERTION: loom's map legend shows %q, guios does not.\nguios: %s", w, texts)
		}
	}
	if strings.Contains(texts, "inline picture") {
		t.Errorf("ASSERTION: the data: image shows its alt text, not the picture")
	}
	if !hasColour(tester.Image(), color.NRGBA{R: 250, G: 20, B: 200, A: 255}) {
		t.Errorf("ASSERTION: the data: image's pixels are not drawn")
	}
	if !strings.Contains(texts, "cannot draw this diagram") || !strings.Contains(texts, "this is not a diagram") {
		t.Errorf("ASSERTION: a diagram that cannot be drawn does not show its note and source.\nguios: %s", texts)
	}
}

// TestTMLBarHeightsAreTheValues: each column of a bar chart is as tall as
// its row's y field says, against the other columns.
func TestTMLBarHeightsAreTheValues(t *testing.T) {
	doc := checkTML(t, `<tml version="1">
  <state>{"rows": [{"name": "alpha", "n": 2, "other": 9}, {"name": "beta", "n": 8, "other": 1}]}</state>
  <bar data="state.rows" x="name" y="n" role="error"/>
</tml>`)
	a, _ := tmlApp(t, doc)
	var pal *palette
	tester := ui.NewTester(func(c *ui.Context) {
		pal = paletteFor(c)
		a.paneTML(c, pal, &proto.Pane{Surface: "s1"})
	}, 600, 400)
	tester.Frame()
	tester.Frame()
	runs := columnRuns(tester.Image(), pal.danger)
	if len(runs) != 2 {
		t.Fatalf("found %d columns in the error colour, want 2: %v", len(runs), runs)
	}
	ratio := float64(runs[1]) / float64(runs[0])
	if ratio < 3.6 || ratio > 4.4 {
		t.Errorf("ASSERTION: beta's column is %.2f times alpha's, want 4 (8 over 2): heights %v", ratio, runs)
	}
}

func checkTML(t *testing.T, src string) *tml.Node {
	t.Helper()
	doc, _, err := tml.Check([]byte(src), nil)
	if err != nil {
		t.Fatalf("the document does not check: %v", err)
	}
	return doc
}

// testPNG is a 16 by 16 picture of one colour as a data: URI.
func testPNG(t *testing.T, c color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func near(a, b uint8) bool { return a-b < 6 || b-a < 6 }

func hasColour(img *image.RGBA, c color.NRGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if near(img.Pix[i], c.R) && near(img.Pix[i+1], c.G) && near(img.Pix[i+2], c.B) {
			return true
		}
	}
	return false
}

// columnRuns is the height of each column of pixels in colour c, one per
// run of such columns from left to right: the bars of a chart.
func columnRuns(img *image.RGBA, c ui.Color) []int {
	b := img.Bounds()
	var runs []int
	inRun := false
	for x := b.Min.X; x < b.Max.X; x++ {
		h := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			o := img.PixOffset(x, y)
			if near(img.Pix[o], c.R) && near(img.Pix[o+1], c.G) && near(img.Pix[o+2], c.B) {
				h++
			}
		}
		switch {
		case h > 2 && !inRun:
			runs, inRun = append(runs, h), true
		case h > 2:
			runs[len(runs)-1] = max(runs[len(runs)-1], h)
		default:
			inRun = false
		}
	}
	return runs
}
