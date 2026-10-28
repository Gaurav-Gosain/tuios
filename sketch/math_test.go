// Ported from pele tests/render/math.test.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele's resolver returns null or a malformed object for "nothing". A Go
// resolver reports false, or gives a width or height that is not a positive
// number. A resolver that throws makes pele's render throw; in sketch it
// panics and Render returns an error.

package sketch

import (
	"cmp"
	"math"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type asked struct {
	tex  string
	size float64
}

type mathRecorder struct {
	asked  []asked
	markup func(tex string, size float64) (MathMarkup, bool)
}

func (r *mathRecorder) math(tex string, size float64) (MathMarkup, bool) {
	r.asked = append(r.asked, asked{tex, size})
	return r.markup(tex, size)
}

// recorder records what it is asked. With no markup it draws a path as wide as the TeX, in a box of 30 by 18.
func recorder(markup func(tex string, size float64) (MathMarkup, bool)) *mathRecorder {
	if markup == nil {
		markup = func(tex string, _ float64) (MathMarkup, bool) {
			return MathMarkup{Markup: `<path d="M0,0H` + strconv.Itoa(len(tex)) + `"/>`, Width: 30, Height: 18}, true
		}
	}
	return &mathRecorder{markup: markup}
}

func (r *mathRecorder) texs() []string {
	out := make([]string, len(r.asked))
	for i, a := range r.asked {
		out[i] = a.tex
	}
	return out
}

var reMathBox = regexp.MustCompile(`<(?:svg|foreignObject) class="pele-math"[^>]*>`)

func boxes(svg string) []string { return reMathBox.FindAllString(svg, -1) }

func mathSVG(t *testing.T, src string, opts RenderOptions) SVG {
	t.Helper()
	out, err := Render(src, opts)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	if err := assertWellFormed(out.Data); err != nil {
		t.Fatalf("%q: not well formed: %v", src, err)
	}
	return out
}

func number(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMathInLabels(t *testing.T) {
	t.Run("leaves $$ as text without a resolver", func(t *testing.T) {
		svg := mathSVG(t, "flowchart LR\n  A[\"$$x^2$$\"]", RenderOptions{}).Data
		if !strings.Contains(svg, "$$x^2$$") || strings.Contains(svg, "pele-math") {
			t.Error(svg)
		}
	})

	t.Run("draws the same as without a resolver when the resolver has nothing", func(t *testing.T) {
		src := "flowchart LR\n  A[\"$$x^2$$ and text\"] -->|\"$$y$$\"| B"
		plain := mathSVG(t, src, RenderOptions{}).Data
		for _, resolve := range []MathResolver{
			func(string, float64) (MathMarkup, bool) { return MathMarkup{}, false },
			func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: nan(), Height: 10}, true },
			func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 10, Height: -1}, true },
			func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: math.Inf(1), Height: 10}, true },
		} {
			if got := mathSVG(t, src, RenderOptions{Math: resolve}).Data; got != plain {
				t.Errorf("differs from the plain drawing:\n%s\n%s", got, plain)
			}
		}
	})

	t.Run("gives the resolver the TeX as written and the label font size", func(t *testing.T) {
		r := recorder(nil)
		mathSVG(t, "flowchart LR\n  A[\"$$a \\neq b_1 < c * d$$\"] -->|\"$$e^#35;$$\"| B", RenderOptions{Math: r.math})
		want := []asked{{`a \neq b_1 < c * d`, 16}, {"e^#", 14}}
		if !slices.Equal(r.asked, want) {
			t.Errorf("asked %v, want %v", r.asked, want)
		}
	})

	t.Run("asks once for an expression used again at the same size", func(t *testing.T) {
		r := recorder(nil)
		mathSVG(t, "flowchart LR\n  A[\"$$x$$\"] --> B[\"$$x$$\"] --> C[\"$$x$$\"]", RenderOptions{Math: r.math})
		if want := []asked{{"x", 16}}; !slices.Equal(r.asked, want) {
			t.Errorf("asked %v", r.asked)
		}
	})

	t.Run("draws SVG markup in a box of the size the resolver gives", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) {
			return MathMarkup{Markup: `<path d="M1,1H9"/>`, Width: 40, Height: 50}, true
		})
		drawn := mathSVG(t, "flowchart LR\n  A[\"$$\\frac{a}{b}$$\"]", RenderOptions{Math: r.math})
		plain := mathSVG(t, "flowchart LR\n  A[\"x\"]", RenderOptions{})
		if got := boxes(drawn.Data); !slices.Equal(got, []string{`<svg class="pele-math" x="-20" y="-25" width="40" height="50" overflow="visible">`}) {
			t.Errorf("boxes %v", got)
		}
		if !strings.Contains(drawn.Data, `overflow="visible"><path d="M1,1H9"/></svg>`) || strings.Contains(drawn.Data, "$$") {
			t.Error(drawn.Data)
		}
		if !(drawn.Height > plain.Height+25) {
			t.Errorf("height %v, plain %v", drawn.Height, plain.Height)
		}
	})

	t.Run("draws HTML markup in a foreignObject at the label font size", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) {
			return MathMarkup{Markup: `<span class="katex">x<br></span>`, HTML: true, Width: 20, Height: 18}, true
		})
		out, err := Render("flowchart LR\n  A[\"$$x$$\"]", RenderOptions{Math: r.math})
		if err != nil {
			t.Fatal(err)
		}
		if got := boxes(out.Data); !slices.Equal(got, []string{`<foreignObject class="pele-math" x="-10" y="-9" width="20" height="18" overflow="visible">`}) {
			t.Errorf("boxes %v", got)
		}
		if !regexp.MustCompile(`<div xmlns="http://www.w3.org/1999/xhtml" style="[^"]*font-size:16px;[^"]*"><span class="katex">x<br></span></div></foreignObject>`).MatchString(out.Data) {
			t.Error(out.Data)
		}
	})

	t.Run("sets math on the line among the text around it", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 30, Height: 40}, true })
		svg := mathSVG(t, "flowchart LR\n  A[\"Total $$x$$ items<br/>last line\"]", RenderOptions{Math: r.math}).Data
		total := regexp.MustCompile(`<tspan x="([-\d.]+)" y="([-\d.]+)" text-anchor="start">Total </tspan>`).FindStringSubmatch(svg)
		items := regexp.MustCompile(`<tspan x="([-\d.]+)" y="([-\d.]+)" text-anchor="start"> items</tspan>`).FindStringSubmatch(svg)
		box := regexp.MustCompile(`<svg class="pele-math" x="([-\d.]+)" y="([-\d.]+)" width="30" height="40"`).FindStringSubmatch(svg)
		last := regexp.MustCompile(`<tspan x="0" y="([-\d.]+)">last line</tspan>`).FindStringSubmatch(svg)
		if total == nil || items == nil || box == nil || last == nil {
			t.Fatal(svg)
		}
		if number(t, items[1])-number(t, box[1]) != 30 {
			t.Errorf("items at %s, box at %s", items[1], box[1])
		}
		if total[2] != items[2] {
			t.Errorf("baselines %s and %s", total[2], items[2])
		}
		middle := number(t, box[2]) + 20
		if math.Abs(number(t, total[2])-16*0.35-middle) >= 0.02 {
			t.Errorf("text at %s, math middle %v", total[2], middle)
		}
		if !(number(t, last[1]) > number(t, box[2])+40) {
			t.Errorf("last line at %s, box at %s", last[1], box[2])
		}
	})

	t.Run("keeps an expression whole when a label wraps", func(t *testing.T) {
		r := recorder(func(tex string, _ float64) (MathMarkup, bool) {
			return MathMarkup{Width: float64(len(tex) * 10), Height: 18}, true
		})
		svg := mathSVG(t, "flowchart TD\n  A[\"some words before $$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa$$ and after it\"]", RenderOptions{
			Math:   r.math,
			Config: Config{"flowchart": map[string]any{"wrappingWidth": 120.0}},
		}).Data
		if b := boxes(svg); len(b) != 1 || !strings.Contains(b[0], `width="300"`) {
			t.Errorf("boxes %v", b)
		}
	})

	t.Run("draws math in sequence messages, participants and notes", func(t *testing.T) {
		r := recorder(nil)
		svg := mathSVG(t, "sequenceDiagram\n  participant A as $$\\alpha$$\n  A->>B: Solve $$x^2$$\n  Note over A: $$\\int x$$", RenderOptions{Math: r.math}).Data
		got := r.texs()
		slices.Sort(got)
		if want := []string{`\alpha`, `\int x`, "x^2"}; !slices.Equal(got, want) {
			t.Errorf("asked %v", got)
		}
		if strings.Contains(svg, "$$") {
			t.Error(svg)
		}
	})

	t.Run("draws math in swimlanes", func(t *testing.T) {
		r := recorder(nil)
		mathSVG(t, "swimlane-beta LR\n  subgraph L[\"$$\\lambda$$\"]\n    A[\"$$a$$\"]\n  end", RenderOptions{Math: r.math})
		got := r.texs()
		slices.Sort(got)
		if want := []string{`\lambda`, "a"}; !slices.Equal(got, want) {
			t.Errorf("asked %v", got)
		}
	})

	t.Run("leaves other diagram types alone", func(t *testing.T) {
		r := recorder(nil)
		src := "pie\n  \"$$x$$\" : 3"
		if mathSVG(t, src, RenderOptions{Math: r.math}).Data != mathSVG(t, src, RenderOptions{}).Data {
			t.Error("pie drawn differently with a resolver")
		}
		mathSVG(t, "timeline\n  2020 : $$x$$", RenderOptions{Math: r.math})
		mathSVG(t, "gantt\n  section S\n    $$x$$ :a1, 2024-01-01, 3d", RenderOptions{Math: r.math, Now: goldenNow})
		if len(r.asked) != 0 {
			t.Errorf("asked %v", r.asked)
		}
	})

	t.Run("colors math as the label text", func(t *testing.T) {
		r := recorder(nil)
		svg := mathSVG(t, "flowchart LR\n  A[\"$$x$$\"]\n  classDef red color:#c00\n  class A red", RenderOptions{Math: r.math}).Data
		if b := boxes(svg); len(b) == 0 || !strings.Contains(b[0], ` color="#c00"`) {
			t.Errorf("boxes %v", b)
		}
	})

	t.Run("colors math in a styled lane as its title", func(t *testing.T) {
		r := recorder(nil)
		svg := mathSVG(t, "swimlane-beta LR\n  subgraph L[\"$$x$$\"]\n    A\n  end\n  style L color:#c00", RenderOptions{Math: r.math}).Data
		if b := boxes(svg); len(b) == 0 || !strings.Contains(b[0], ` color="#c00"`) {
			t.Errorf("boxes %v", b)
		}
	})

	t.Run("draws a cluster title once, not again in its outline", func(t *testing.T) {
		r := recorder(nil)
		svg := mathSVG(t, "flowchart LR\n  subgraph S[\"$$x$$\"]\n    A\n  end", RenderOptions{Math: r.math}).Data
		if b := boxes(svg); len(b) != 1 || !strings.Contains(b[0], ` color="var(--_m)"`) {
			t.Errorf("boxes %v", b)
		}
	})

	t.Run("matches each expression lazily, within one line", func(t *testing.T) {
		r := recorder(nil)
		mathSVG(t, "flowchart LR\n  A[\"$$a$$ and $$b$$<br/>$$c<br/>d$$\"]", RenderOptions{Math: r.math})
		if want := []string{"a", "b", "c<br/>d"}; !slices.Equal(r.texs(), want) {
			t.Errorf("asked %v", r.texs())
		}
	})

	t.Run("keeps each expression in its place when a tag drops another", func(t *testing.T) {
		r := recorder(func(tex string, _ float64) (MathMarkup, bool) {
			return MathMarkup{Markup: `<g id="` + tex + `"/>`, Width: 10, Height: 18}, true
		})
		svg := mathSVG(t, "flowchart LR\n  A[\"<span title=$$a$$>hello</span> $$b$$ $$c$$\"]", RenderOptions{Math: r.math}).Data
		if strings.Contains(svg, `<g id="a"/>`) {
			t.Error(svg)
		}
		b := regexp.MustCompile(`<svg class="pele-math" x="([-\d.]+)"[^>]*><g id="b"/>`).FindStringSubmatch(svg)
		c := regexp.MustCompile(`<svg class="pele-math" x="([-\d.]+)"[^>]*><g id="c"/>`).FindStringSubmatch(svg)
		if b == nil || c == nil || !(number(t, c[1]) > number(t, b[1])) {
			t.Error(svg)
		}
	})

	t.Run("shows unresolved expressions as TeX among resolved ones in markdown", func(t *testing.T) {
		r := recorder(func(tex string, _ float64) (MathMarkup, bool) {
			if tex == "b" {
				return MathMarkup{}, false
			}
			return MathMarkup{Width: 10, Height: 18}, true
		})
		svg := mathSVG(t, "flowchart LR\n  A[\"`**bold** $$a_1$$ and $$b$$ *c*`\"]", RenderOptions{Math: r.math}).Data
		if len(boxes(svg)) != 1 || !strings.Contains(svg, "$$b$$") || !strings.Contains(svg, `font-weight="var(--_w)"`) || !strings.Contains(svg, `font-style="italic"`) {
			t.Error(svg)
		}
	})

	t.Run("gives math the label opacity, but not styles that would change its size", func(t *testing.T) {
		r := recorder(nil)
		svg := mathSVG(t, "flowchart LR\n  A[\"Hide $$x$$\"]\n  style A opacity:0,letter-spacing:6px,word-spacing:4px", RenderOptions{Math: r.math}).Data
		if b := boxes(svg); len(b) == 0 || !strings.Contains(b[0], ` opacity="0"`) || strings.Contains(b[0], "spacing") {
			t.Errorf("boxes %v", b)
		}
	})

	t.Run("draws a patch behind math in a cluster title an edge runs through", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) {
			return MathMarkup{Markup: `<g id="title"/>`, Width: 200, Height: 18}, true
		})
		svg := mathSVG(t, "flowchart TD\n  X --> A\n  subgraph S[\"$$x$$\"]\n    A\n  end", RenderOptions{Math: r.math}).Data
		at := strings.Index(svg, `<g class="pele-cluster-title"`)
		if at < 0 {
			t.Fatal(svg)
		}
		title := svg[at:]
		if end := strings.Index(title, `<g class="pele-node`); end >= 0 {
			title = title[:end]
		}
		if !regexp.MustCompile(`<rect x="[-\d.]+" y="[-\d.]+" width="200" height="18" rx="2" fill="color-mix\(`).MatchString(title) {
			t.Error(title)
		}
		if n := strings.Count(svg, `<g id="title"/>`); n != 1 {
			t.Errorf("%d titles", n)
		}
	})

	t.Run("is not confused by a render inside the resolver", func(t *testing.T) {
		icons := func(name string) string { return `<path data-name="` + name + `"/>` }
		resolve := func(tex string, _ float64) (MathMarkup, bool) {
			if _, err := Render("flowchart LR\n  B[\"fa:fa-star <b></b> $$y$$\"]", RenderOptions{Icons: icons}); err != nil {
				t.Error(err)
			}
			return MathMarkup{Markup: `<g id="` + tex + `"/>`, Width: 10, Height: 18}, true
		}
		svg := mathSVG(t, "flowchart LR\n  A[\"$$x$$ fa:fa-car\"]", RenderOptions{Icons: icons, Math: resolve}).Data
		if strings.Count(svg, `data-icon="fa:fa-car"`) != 1 || strings.Contains(svg, "fa-star") || !strings.Contains(svg, `<g id="x"/>`) {
			t.Error(svg)
		}
	})

	t.Run("recovers when the resolver panics", func(t *testing.T) {
		src := "flowchart LR\n  A[\"$$x$$\"]"
		if _, err := Render(src, RenderOptions{Math: func(string, float64) (MathMarkup, bool) { panic("no") }}); err == nil || !strings.Contains(err.Error(), "no") {
			t.Errorf("error %v", err)
		}
		mathSVG(t, "flowchart LR\n  A[\"<b></b>\"]", RenderOptions{})
		if b := boxes(mathSVG(t, src, RenderOptions{Math: recorder(nil).math}).Data); len(b) != 1 {
			t.Errorf("boxes %v", b)
		}
	})

	t.Run("makes room for tall math in a sequence message that breaks", func(t *testing.T) {
		tall := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 40, Height: 60}, true })
		short := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 40, Height: 10}, true })
		src := "sequenceDiagram\n  A->>B: averyveryverylongwordthatmustbreak $$x$$ more\n  B->>A: ok"
		w := 220.0
		if d := mathSVG(t, src, RenderOptions{MaxWidth: &w, Math: tall.math}).Height - mathSVG(t, src, RenderOptions{MaxWidth: &w, Math: short.math}).Height; d < 40 {
			t.Errorf("tall math adds %v", d)
		}
	})
}

func nan() float64 { return math.NaN() }

var mathCoverage = []struct {
	typ, src string
	want     []string
}{
	{"state", "stateDiagram-v2\n  state \"$$a$$\" as s1\n  s1 : $$b$$\n  [*] --> s1 : $$c$$\n  note right of s1 : $$d$$\n  state \"$$e$$\" as C {\n    x --> y\n  }", []string{"a", "b", "c", "d", "e"}},
	{"class", "classDiagram\n  class A {\n    <<$$a$$>>\n  }\n  note for A \"$$b$$\"\n  A \"$$c$$\" --> \"1\" B : $$d$$\n  class C[\"$$e$$\"]", []string{"a", "b", "c", "d", "e"}},
	{"er", "erDiagram\n  p[\"$$a$$\"] {\n    string name \"$$b$$\"\n  }\n  p ||--o{ q : \"$$c$$\"", []string{"a", "b", "c"}},
	{"mindmap", "mindmap\n  root((\"$$a$$\"))\n    $$b$$", []string{"a", "b"}},
	{"kanban", "kanban\n  todo[\"$$a$$\"]\n    t1[\"$$b$$\"]", []string{"a", "b"}},
	{"block", "block-beta\n  a[\"$$a$$\"] b[\"$$b$$\"]\n  a -- \"$$c$$\" --> b", []string{"a", "b", "c"}},
	{"requirement", "requirementDiagram\n  requirement \"$$a$$\" {\n    id: \"$$b$$\"\n    text: \"$$c$$\"\n  }\n  element e1 {\n    type: \"$$d$$\"\n    docref: \"$$e$$\"\n  }", []string{"a", "b", "c", "d", "e"}},
	{"usecase", "usecase-beta\n  actor C(\"$$a$$\")\n  systemBoundary \"$$b$$\"\n    U(\"$$c$$\")\n  end\n  C --> U", []string{"a", "b", "c"}},
}

var (
	reAttrValue = regexp.MustCompile(`"[^"]*"`)
	reMathSpans = regexp.MustCompile(`<svg class="pele-math" x="([-\d.]+)" y="([-\d.]+)" width="([\d.]+)" height="([\d.]+)"`)
)

func mathSpans(t *testing.T, svg string) [][4]float64 {
	var out [][4]float64
	for _, m := range reMathSpans.FindAllStringSubmatch(svg, -1) {
		out = append(out, [4]float64{number(t, m[1]), number(t, m[2]), number(t, m[3]), number(t, m[4])})
	}
	return out
}

func TestMathInOtherTypes(t *testing.T) {
	for _, c := range mathCoverage {
		t.Run("typesets the labels Mermaid typesets in "+c.typ+" diagrams", func(t *testing.T) {
			r := recorder(nil)
			svg := mathSVG(t, c.src, RenderOptions{Math: r.math}).Data
			got := slices.Compact(slices.Sorted(slices.Values(r.texs())))
			if !slices.Equal(got, c.want) {
				t.Errorf("asked %v", got)
			}
			// Ignore attributes: IDs retain the TeX source.
			if strings.Contains(reAttrValue.ReplaceAllString(svg, `""`), "$$") {
				t.Error(svg)
			}
		})
	}

	t.Run("leaves class members as text, as Mermaid does", func(t *testing.T) {
		r := recorder(nil)
		svg := mathSVG(t, "classDiagram\n  class A {\n    +$$m$$ int\n  }\n  A : $$n$$ text", RenderOptions{Math: r.math}).Data
		if len(r.asked) != 0 || !strings.Contains(svg, "$$m$$ int") || !strings.Contains(svg, "$$n$$ text") {
			t.Errorf("asked %v\n%s", r.asked, svg)
		}
	})

	t.Run("leaves titles as text, as Mermaid does", func(t *testing.T) {
		for _, body := range []string{"flowchart LR\n  A", "sequenceDiagram\n  A->>B: hi", "stateDiagram-v2\n  [*] --> s1", "erDiagram\n  A ||--o{ B : has", "kanban\n  todo[Todo]", "mindmap\n  root"} {
			r := recorder(nil)
			svg := mathSVG(t, "---\ntitle: $$t$$\n---\n"+body, RenderOptions{Math: r.math}).Data
			if len(r.asked) != 0 || !strings.Contains(svg, "$$t$$") {
				t.Errorf("%s: asked %v", body, r.asked)
			}
		}
	})

	tall := []struct{ typ, src string }{
		{"er", "erDiagram\n  p {\n    string name \"$$t$$<br/>after\"\n  }"},
		{"mindmap", "mindmap\n  root\n    child[\"$$t$$<br/>after\"]"},
		{"kanban", "kanban\n  todo[Todo]\n    t1[\"$$t$$<br/>after\"]"},
		{"requirement", "requirementDiagram\n  requirement r1 {\n    id: 1\n    text: \"$$t$$<br/>after\"\n  }"},
		{"usecase", "usecase-beta\n  actor C(\"C\")\n  U(\"`$$t$$\nafter`\")\n  C --> U"},
		{"state", "stateDiagram-v2\n  s1 : $$t$$<br/>after"},
		{"class", "classDiagram\n  class A\n  note for A \"$$t$$<br/>after\""},
	}
	reBox := regexp.MustCompile(`<svg class="pele-math" x="[-\d.]+" y="([-\d.]+)" width="30" height="60"`)
	reAfter := regexp.MustCompile(`y="([-\d.]+)"[^>]*>after<`)
	for _, c := range tall {
		t.Run("puts the line after tall math below it in "+c.typ+" diagrams", func(t *testing.T) {
			r := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 30, Height: 60}, true })
			svg := mathSVG(t, c.src, RenderOptions{Math: r.math}).Data
			box := reBox.FindStringSubmatch(svg)
			after := reAfter.FindStringSubmatch(svg)
			if box == nil || after == nil {
				t.Fatal(svg)
			}
			if !(number(t, after[1]) > number(t, box[1])+60) {
				t.Errorf("after at %s, box at %s", after[1], box[1])
			}
		})
	}

	t.Run("makes room between classes for tall cardinalities at both ends of an edge", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 20, Height: 40}, true })
		svg := mathSVG(t, "classDiagram\n  A \"$$x$$\" --> \"$$y$$\" B", RenderOptions{Math: r.math}).Data
		spans := mathSpans(t, svg)
		if len(spans) != 2 {
			t.Fatal(svg)
		}
		slices.SortFunc(spans, func(a, b [4]float64) int { return cmp.Compare(a[1], b[1]) })
		if !(spans[0][1]+spans[0][3] <= spans[1][1]) {
			t.Errorf("spans %v", spans)
		}
	})

	t.Run("gives a JSON row in a use case diagram the height of its key", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 20, Height: 40}, true })
		svg := mathSVG(t, "usecase-beta\n  json Payload@{\"$$x$$\":1,\"other\":2}", RenderOptions{Math: r.math}).Data
		spans := mathSpans(t, svg)
		other := regexp.MustCompile(`y="([-\d.]+)"[^>]*>other<`).FindStringSubmatch(svg)
		if len(spans) == 0 || other == nil {
			t.Fatal(svg)
		}
		if !(number(t, other[1])-14*0.35-14*0.75 >= spans[0][1]+spans[0][3]) {
			t.Errorf("other at %s, key %v", other[1], spans[0])
		}
	})

	t.Run("widens kanban columns to hold math that cannot wrap", func(t *testing.T) {
		r := recorder(func(string, float64) (MathMarkup, bool) { return MathMarkup{Width: 300, Height: 30}, true })
		reCard := regexp.MustCompile(`<rect width="([\d.]+)" height="[\d.]+" fill="var\(--_bg\)"`)
		src := "kanban\n  todo[Todo]\n    a[\"$$x$$\"]\n  done[Done]\n    b[Other]"
		svg := mathSVG(t, src, RenderOptions{Math: r.math}).Data
		card := reCard.FindStringSubmatch(svg)
		spans := mathSpans(t, svg)
		if card == nil || len(spans) == 0 || !(spans[0][0]+spans[0][2] <= number(t, card[1])) {
			t.Errorf("card %v, spans %v", card, spans)
		}
		if plain := mathSVG(t, "kanban\n  todo[Todo]\n    a[plain]", RenderOptions{}).Data; !strings.Contains(plain, `<rect width="184"`) {
			t.Error(plain)
		}
		w := 240.0
		narrow := mathSVG(t, src, RenderOptions{Math: r.math, MaxWidth: &w}).Data
		narrowCard := reCard.FindStringSubmatch(narrow)
		spans = mathSpans(t, narrow)
		if narrowCard == nil || len(spans) == 0 || !(spans[0][0]+spans[0][2] <= number(t, narrowCard[1])) {
			t.Errorf("card %v, spans %v", narrowCard, spans)
		}
	})
}

// TestMathLabelsStayLinear renders labels of 200,000 characters full of $$,
// with a resolver, as longRunsStayLinear does for the parsers. Not part of pele.
// At most 6,400 expressions are marked, which keeps a quadratic join under the
// bound of the parsers' test, so the bound here is lower: pushing each piece of
// TeX source on its own allocates 266 MB for "expressions, untypeset".
func TestMathLabelsStayLinear(t *testing.T) {
	const n = 200_000
	const bound = 128 << 20
	none := func(string, float64) (MathMarkup, bool) { return MathMarkup{}, false }
	some := func(string, float64) (MathMarkup, bool) {
		return MathMarkup{Markup: "<g/>", Width: 1, Height: 18}, true
	}
	label := func(body string) string { return "flowchart LR\n  A[\"`" + body + "`\"]" }
	for name, src := range map[string]string{
		"expressions":         label(strings.Repeat("$$a$$ ", n/6)),
		"expressions in tags": label(strings.Repeat("$$a$$<b>x</b>", n/13)),
		"unclosed":            label("$$" + strings.Repeat("a", n)),
		"unclosed lines":      label(strings.Repeat("$$"+strings.Repeat("a", 50)+"\n", n/53)),
		"dollars":             label(strings.Repeat("$", n)),
	} {
		for kind, resolve := range map[string]MathResolver{"untypeset": none, "typeset": some} {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, err := Render(src, RenderOptions{Limit: 1 << 20, OutputLimit: 1 << 30, Math: resolve})
			took := time.Since(start)
			runtime.ReadMemStats(&after)
			got := after.TotalAlloc - before.TotalAlloc
			t.Logf("%s, %s: %d MB, %v, error %v", name, kind, got>>20, took, err)
			if got > bound {
				t.Errorf("%s, %s, of %d characters: %d MB allocated, want at most %d MB", name, kind, len(src), got>>20, bound>>20)
			}
		}
	}
}
