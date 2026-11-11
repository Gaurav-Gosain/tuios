package sketch

// Ported from pele tests/render/palette.test.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac:
// the palette theme variables, the colors they may hold and the label colors on them.

import (
	"encoding/json"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	reRootStyle   = regexp.MustCompile(`^<svg[^>]*\bstyle="([^"]*)"`)
	reSeriesDecl  = regexp.MustCompile(`--pele-series-(\d):([^;]*)`)
	reSliceLabels = regexp.MustCompile(`<text [^>]*>\d+%<`)
)

func themed(t *testing.T, vars map[string]any, body string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"themeVariables": vars})
	if err != nil {
		t.Fatal(err)
	}
	return "%%{init: " + string(data) + "}%%\n" + body
}

func series(t *testing.T, svg string) map[int]string {
	t.Helper()
	found := map[int]string{}
	for _, m := range reSeriesDecl.FindAllStringSubmatch(reRootStyle.FindStringSubmatch(svg)[1], -1) {
		n, _ := strconv.Atoi(m[1])
		found[n] = m[2]
	}
	return found
}

func wantSeries(t *testing.T, what string, got, want map[int]string) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Errorf("%s: series %v, want %v", what, got, want)
	}
}

const (
	palettePie     = "pie\n  \"A\": 1\n  \"B\": 2"
	paletteXYChart = "xychart\n  bar [1, 2]\n  line [2, 1]"
)

func TestPaletteThemeVariables(t *testing.T) {
	t.Run("colors xychart series from plotColorPalette, repeating a short list", func(t *testing.T) {
		source := `---
config:
  themeVariables:
    xyChart:
      plotColorPalette: '#000000, #0000FF, #00FF00, #FF0000'
---
xychart
title "Different Colors in xyChart"
x-axis "categoriesX" ["Category 1", "Category 2", "Category 3", "Category 4"]
y-axis "valuesY" 0 --> 50
line [10,20,30,40]
bar [20,30,25,35]
bar [15,25,20,30]
line [5,15,25,35]`
		svg := renderSVG(t, source, RenderOptions{})
		wantSeries(t, "xychart", series(t, svg), map[int]string{
			1: "#000000", 2: "#0000FF", 3: "#00FF00", 4: "#FF0000",
			5: "#000000", 6: "#0000FF", 7: "#00FF00", 8: "#FF0000",
		})
		if !strings.Contains(svg, `stroke="var(--pele-series-1,`) {
			t.Error("the lines do not take the series colors")
		}
	})

	t.Run("keeps commas inside color functions", func(t *testing.T) {
		got := series(t, renderSVG(t, themed(t, map[string]any{"xyChart": map[string]any{"plotColorPalette": "rgb(0, 0, 0), hsl(0 50% 50% / 0.5)"}}, paletteXYChart), RenderOptions{}))
		if got[1] != "rgb(0, 0, 0)" || got[2] != "hsl(0 50% 50% / 0.5)" {
			t.Errorf("series %v", got)
		}
	})

	t.Run("maps each diagram palette onto the series in order", func(t *testing.T) {
		for _, c := range []struct {
			body string
			vars map[string]any
		}{
			{palettePie, map[string]any{"pie1": "red", "pie2": "blue"}},
			{"gitGraph\n  commit", map[string]any{"git0": "red", "git1": "blue"}},
			{"timeline\n  2001 : a\n  2002 : b", map[string]any{"cScale0": "red", "cScale1": "blue"}},
			{"radar-beta\n  axis a, b, c\n  curve x{1,2,3}", map[string]any{"cScale0": "red", "cScale1": "blue"}},
			{"treemap\n\"a\": 1\n\"b\": 2", map[string]any{"cScale0": "red", "cScale1": "blue"}},
			{"mindmap\n  root\n    a\n    b", map[string]any{"cScale0": "green", "cScale1": "red", "cScale2": "blue"}},
		} {
			wantSeries(t, c.body, series(t, renderSVG(t, themed(t, c.vars, c.body), RenderOptions{})), map[int]string{1: "red", 2: "blue"})
		}
	})

	t.Run("repeats the venn colors that are set", func(t *testing.T) {
		svg := renderSVG(t, themed(t, map[string]any{"venn2": "red", "venn4": "blue"}, "venn-beta\n  set A\n  set B"), RenderOptions{})
		wantSeries(t, "venn", series(t, svg), map[int]string{1: "red", 2: "blue", 3: "red", 4: "blue", 5: "red", 6: "blue", 7: "red", 8: "blue"})
	})

	t.Run("ignores palettes of other diagrams and other theme colors", func(t *testing.T) {
		none := map[int]string{}
		wantSeries(t, "pie", series(t, renderSVG(t, themed(t, map[string]any{"cScale0": "red", "primaryColor": "red"}, palettePie), RenderOptions{})), none)
		wantSeries(t, "journey", series(t, renderSVG(t, themed(t, map[string]any{"fillType0": "red", "cScale0": "red"}, "journey\n  section S\n    Task: 5: Me"), RenderOptions{})), none)
		wantSeries(t, "flowchart", series(t, renderSVG(t, themed(t, map[string]any{"pie1": "red"}, "flowchart LR\n  a --> b"), RenderOptions{})), none)
	})

	t.Run("reads theme variables from front matter, directives and the config option", func(t *testing.T) {
		front := "---\nconfig:\n  themeVariables:\n    pie1: '#123456'\n---\n" + palettePie
		wantSeries(t, "front matter", series(t, renderSVG(t, front, RenderOptions{})), map[int]string{1: "#123456"})
		fromOption := renderSVG(t, palettePie, RenderOptions{Config: map[string]any{"themeVariables": map[string]any{"pie2": "oklch(70% 0.1 200)"}}})
		wantSeries(t, "config option", series(t, fromOption), map[int]string{2: "oklch(70% 0.1 200)"})
	})

	t.Run("accepts theme-aware colors", func(t *testing.T) {
		vars := map[string]any{"pie1": "var(--pele-accent)", "pie2": "light-dark(#000, #fff)", "pie3": "color-mix(in srgb, red 40%, blue)"}
		wantSeries(t, "pie", series(t, renderSVG(t, themed(t, vars, palettePie), RenderOptions{})), map[int]string{
			1: "var(--pele-accent)",
			2: "light-dark(#000, #fff)",
			3: "color-mix(in srgb, red 40%, blue)",
		})
	})

	t.Run("drops values that are not colors and keeps the theme color in their place", func(t *testing.T) {
		for _, value := range []string{
			"url(https://example.com/a.svg)",
			"url(//example.com/a.svg)",
			"image-set(a 1x)",
			"red;stroke:blue",
			`"><script>alert(1)</script>`,
			"expression(alert(1))",
			"rgb(0, 0",
			"red)",
			"red}",
			"javascript:alert(1)",
			"/*",
			"reddish",
			"#ff00000",
			"var(--page-color)",
			"color-mix(in srgb, var(--page-color) 50%, red)",
		} {
			svg := renderSVG(t, themed(t, map[string]any{"pie1": value, "pie2": "blue"}, palettePie), RenderOptions{})
			wantSeries(t, value, series(t, svg), map[int]string{2: "blue"})
			if strings.Contains(svg, "<script") || strings.Contains(svg, "url(") {
				t.Errorf("%q gets into the SVG", value)
			}
		}
		list := renderSVG(t, themed(t, map[string]any{"xyChart": map[string]any{"plotColorPalette": "url(a), blue"}}, paletteXYChart), RenderOptions{})
		wantSeries(t, "plotColorPalette", series(t, list), map[int]string{2: "blue", 4: "blue", 6: "blue", 8: "blue"})
	})

	t.Run("picks label colors that contrast with palette colors", func(t *testing.T) {
		const onLight = `fill="var(--pele-fg-on-light,#1f1f1f)"`
		const onDark = `fill="var(--pele-fg-on-dark,#fff)"`
		pie := renderSVG(t, themed(t, map[string]any{"pie1": "#ffeb3b", "pie2": "navy", "pie3": "var(--pele-accent)"}, "pie\n  \"A\": 1\n  \"B\": 1\n  \"C\": 1"), RenderOptions{})
		labels := reSliceLabels.FindAllString(pie, -1)
		if len(labels) != 3 || !strings.Contains(labels[0], onLight) || !strings.Contains(labels[1], onDark) || strings.Contains(labels[2], "fill=") {
			t.Errorf("pie labels %q", labels)
		}
		if !strings.Contains(pie, `class="pele-slice-labels"`) {
			t.Error("pie has no slice labels group")
		}

		git := renderSVG(t, themed(t, map[string]any{"git0": "#ffeb3b"}, "gitGraph\n  commit\n  branch dev\n  commit"), RenderOptions{})
		if !strings.Contains(git, `class="pele-label" `+onLight) || !strings.Contains(git, `class="pele-label" fill="var(--_bg)"`) {
			t.Error("git branch labels do not contrast with their palette colors")
		}

		timeline := renderSVG(t, themed(t, map[string]any{"cScale0": "#ffeb3b"}, "timeline\n  section S\n    2001 : a"), RenderOptions{})
		if n := strings.Count(timeline, onLight); n != 2 {
			t.Errorf("timeline has %d labels on the light palette color, want 2", n)
		}

		treemap := renderSVG(t, themed(t, map[string]any{"cScale0": "#ffeb3b"}, "treemap\n\"a\": 1\n\"b\": 1"), RenderOptions{})
		if n := strings.Count(treemap, onLight); n != 2 {
			t.Errorf("treemap has %d labels on the light palette color, want 2", n)
		}
	})

	t.Run("leaves the output unchanged without a palette", func(t *testing.T) {
		plain := renderSVG(t, palettePie, RenderOptions{})
		if plain != renderSVG(t, themed(t, map[string]any{"primaryColor": "red"}, palettePie), RenderOptions{}) {
			t.Error("a theme variable outside the palette changes the output")
		}
		if strings.Contains(plain, "--pele-series-1:") {
			t.Error("a pie with no palette sets a series color")
		}
	})
}
