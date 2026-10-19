package sketch

// Ported from pele tests/render/{sequence,state,class,er,gantt,mindmap}-security.test.ts,
// the inert part of tests/render/pie.test.ts and tests/support/corpus.ts at
// 458322a5b8abb1ae57ddf127c32017ab9fb14a92. The template lists are in
// security_templates_test.go, written by scripts/security-templates.py.

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tuios.dev/sketch/internal/types"
)

// jsonText is JSON.stringify of a string: no HTML escaping, as in JavaScript.
func jsonText(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

const (
	ganttHead  = "gantt\n  dateFormat YYYY-MM-DD\n"
	ganttTasks = "  section S\n  A task :a1, 2014-01-01, 30d\n  Another :after a1, 20d\n"
)

var reNaN = regexp.MustCompile(`NaN|Infinity`)

// inertTry renders src and fails the test on output that is not inert. It
// reports whether the source rendered: a source that does not parse is fine
// when it fails with an Error, not a panic in the port.
func inertTry(t *testing.T, src string, opt RenderOptions, noNaN bool) bool {
	t.Helper()
	out, err := Render(src, opt)
	if err != nil {
		var pe *Error
		if !errors.As(err, &pe) || strings.HasPrefix(pe.Message, "sketch: internal error") {
			t.Fatalf("%q: %v", trimSrc(src), err)
		}
		return false
	}
	if problem := inertProblem(out.Data); problem != "" {
		t.Fatalf("%q: output is not inert: %s", trimSrc(src), problem)
	}
	if noNaN && reNaN.MatchString(out.Data) {
		t.Fatalf("%q: output holds NaN or Infinity", trimSrc(src))
	}
	return true
}

func trimSrc(s string) string {
	if len(s) > 160 {
		return s[:160]
	}
	return s
}

// corpusOf is the documentation corpus of one diagram type.
func corpusOf(t *testing.T, name string) []string {
	var out []string
	for _, c := range loadGoldens(t) {
		if c.name == name {
			out = append(out, c.source)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no corpus for %s", name)
	}
	return out
}

// lcg is random() of pele's tests/support/corpus.ts.
func lcg(seed uint32) func() float64 {
	state := seed
	return func() float64 {
		state = state*1664525 + 1013904223
		return float64(state) / 4294967296
	}
}

// mutator is mutator() of pele's tests/support/corpus.ts, on UTF-16 units as in
// JavaScript; a cut through a surrogate pair gives U+FFFD, which is harmless here.
func mutator(corpus []string, fragments []string, rnd func() float64) func() string {
	units := func(s string) []uint16 { return utf16Of(s) }
	pickS := func(list []string) string { return list[int(math.Floor(rnd()*float64(len(list))))] }
	return func() string {
		s := units(pickS(corpus))
		edits := 1 + int(math.Floor(rnd()*3))
		for range edits {
			pos := int(math.Floor(rnd() * float64(len(s)+1)))
			r := rnd()
			slice := func(a, b int) []uint16 {
				a, b = max(0, min(a, len(s))), max(0, min(b, len(s)))
				if b < a {
					b = a
				}
				return s[a:b]
			}
			cat := func(parts ...[]uint16) []uint16 {
				var out []uint16
				for _, p := range parts {
					out = append(out, p...)
				}
				return out
			}
			switch {
			case r < 0.35:
				s = cat(slice(0, pos), units(pickS(fragments)), slice(pos, len(s)))
			case r < 0.6:
				s = cat(slice(0, pos), slice(pos+1+int(math.Floor(rnd()*3)), len(s)))
			case r < 0.75:
				other := units(pickS(corpus))
				from := int(math.Floor(rnd() * float64(len(other))))
				n := int(math.Floor(rnd() * 20))
				s = cat(slice(0, pos), other[from:min(len(other), from+n)], slice(pos, len(s)))
			case r < 0.9:
				s = cat(slice(0, pos), slice(pos, pos+int(math.Floor(rnd()*6))), slice(pos, len(s)))
			default:
				s = cat(slice(0, pos), units(pickS(fragments)), slice(pos+1, len(s)))
			}
		}
		return stringOf(s)
	}
}

func utf16Of(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xd800+(r>>10)), uint16(0xdc00+(r&0x3ff)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func stringOf(u []uint16) string {
	var b strings.Builder
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xd800 && c < 0xdc00 && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] < 0xe000 {
			c = 0x10000 + (c-0xd800)<<10 + rune(u[i+1]-0xdc00)
			i++
		}
		b.WriteRune(c)
	}
	return b.String()
}

// mutate checks count mutated sources of the corpus, each at most 4,000 units.
func mutate(t *testing.T, corpus, fragments []string, seed uint32, count int, opt RenderOptions, noNaN bool) int {
	t.Helper()
	if testing.Short() {
		count /= 8
	}
	next := mutator(corpus, append(append([]string{}, PAYLOADS...), fragments...), lcg(seed))
	rendered := 0
	for range count {
		src := next()
		if len(utf16Of(src)) <= 4000 && inertTry(t, src, opt, noNaN) {
			rendered++
		}
	}
	return rendered
}

// hostile renders every template with every payload and wants more than min
// of them to render.
func hostile(t *testing.T, templates []func(string) string, minimum int, opt RenderOptions, noNaN bool, variants ...func(string) string) {
	t.Helper()
	rendered := 0
	for _, template := range templates {
		for _, payload := range PAYLOADS {
			if inertTry(t, template(payload), opt, noNaN) {
				rendered++
			}
			for _, v := range variants {
				if inertTry(t, template(v(payload)), opt, noNaN) {
					rendered++
				}
			}
		}
	}
	if rendered <= minimum {
		t.Fatalf("%d hostile sources rendered, want more than %d", rendered, minimum)
	}
}

// noLinks renders src and wants neither an <a> element nor a link.
func noLinks(t *testing.T, src string, opt RenderOptions) {
	t.Helper()
	out, err := Render(src, opt)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	if regexp.MustCompile(`<a\b`).MatchString(out.Data) || len(out.Links) > 0 {
		t.Fatalf("%q: links %v in\n%s", src, out.Links, out.Data)
	}
}

func hostileOptions(t *testing.T, src string) {
	t.Helper()
	for _, payload := range PAYLOADS {
		if !inertTry(t, src, RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("%q with a hostile id prefix and font: did not render", src)
		}
	}
}

func TestSequenceOutputIsInert(t *testing.T) {
	for _, src := range corpusOf(t, "sequence") {
		inertTry(t, src, RenderOptions{}, false)
	}
	hostile(t, sequenceTemplates, 900, RenderOptions{}, false)
	src := "sequenceDiagram\n  accTitle: t\n  accDescr: d\n  autonumber\n  A->>+B: x\n  Note over A: n\n  B-->>-A: y"
	hostileOptions(t, src)
	for _, payload := range PAYLOADS {
		cfg := Config{"sequence": map[string]any{"messageAlign": payload, "width": payload, "mirrorActors": payload}}
		if !inertTry(t, src, RenderOptions{Config: cfg}, false) {
			t.Fatalf("hostile sequence config %q: did not render", payload)
		}
	}
	for _, value := range []any{math.NaN(), math.Inf(1), math.Inf(-1), -1.0, 0.0, 1e308, 1e-308, "12", nil} {
		cfg := Config{"sequence": map[string]any{"width": value, "height": value, "actorMargin": value,
			"messageMargin": value, "noteMargin": value, "boxMargin": value}}
		src := "sequenceDiagram\n  box B\n  participant A\n  end\n  A->>C: x\n  Note over A,C: n\n  loop l\n  C->>C: y\n  end"
		if !inertTry(t, src, RenderOptions{Config: cfg}, false) {
			t.Fatalf("sequence config value %v: did not render", value)
		}
	}
	if !inertTry(t, "sequenceDiagram\n  autonumber "+strings.Repeat("9", 400)+" "+strings.Repeat("9", 400)+"\n  A->>B: x\n  B->>A: y", RenderOptions{}, false) {
		t.Fatal("huge autonumber: did not render")
	}
	mutate(t, corpusOf(t, "sequence"), []string{"\"", ":", "\n", "@{ \"type\": \"", "\" }", " as ", "rect ", "box ",
		"links A: {\"a\": \"", "properties A: {\"class\": \"", "Note over A: ", "title ", "end\n", "loop ", "<br/>", "wrap: "}, 3, 4000, RenderOptions{}, false)
	noLinks(t, "sequenceDiagram\n  participant A\n  link A: x @ javascript:alert(1)\n  links A: {\"y\": \"javascript:alert(1)\"}\n  A->>B: hi", RenderOptions{})
	out, err := Render("sequenceDiagram\n  rect url(javascript:alert(1))\n  A->>B: hi\n  end\n  rect red\n  B->>A: yo\n  end", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "url(") || !strings.Contains(out.Data, `style="rx:var(--_r);fill:red;"`) {
		t.Fatalf("rect colours do not pass the style filter: %v\n%s", err, out.Data)
	}
}

func TestStateOutputIsInert(t *testing.T) {
	for _, src := range corpusOf(t, "state") {
		inertTry(t, src, RenderOptions{}, false)
	}
	hostile(t, stateTemplates, 500, RenderOptions{}, false)
	hostileOptions(t, "stateDiagram-v2\n  accTitle: t\n  a --> b")
	mutate(t, corpusOf(t, "state"), []string{"\"", ":", ":::", "\n", "{", "}", "--", "-->", "[*]", "state \"", "\" as ",
		"note right of ", "end note", "click a \"", "style a fill:", "classDef c ", "class a ", "<<fork>>", "<<choice>>", "direction LR"}, 3, 4000, RenderOptions{}, false)
	// The last two spell characters as Mermaid entity codes, which are resolved before the check.
	for _, url := range []string{"javascript:alert(1)", " JaVaScRiPt:alert(1)", "data:text/html,x", "vbscript:x", "jav#9;ascript:alert(1)", "#106;avascript#colon;alert(1)"} {
		noLinks(t, "stateDiagram-v2\n  a --> b\n  click a \""+url+"\" \"t\"\n", RenderOptions{})
	}
}

func TestClassOutputIsInert(t *testing.T) {
	for _, src := range corpusOf(t, "class") {
		inertTry(t, src, RenderOptions{}, false)
	}
	hostile(t, classTemplates, 1500, RenderOptions{}, false, func(p string) string { return strings.ReplaceAll(p, `"`, "#quot;") })
	for _, payload := range PAYLOADS {
		if !inertTry(t, "classDiagram\n  accTitle: t\n  A \"1\" --> \"2\" B : x", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatal("hostile id prefix and font: did not render")
		}
		if !inertTry(t, "classDiagram\n  A --> B", RenderOptions{Config: Config{"class": map[string]any{"nodeSpacing": payload, "rankSpacing": payload}}}, false) {
			t.Fatal("hostile class config: did not render")
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 1e308} {
		if !inertTry(t, "classDiagram\n  A \"1\" --> \"2\" B : x", RenderOptions{Config: Config{"class": map[string]any{"nodeSpacing": value, "rankSpacing": value}}}, false) {
			t.Fatalf("class spacing %v: did not render", value)
		}
	}
	for _, url := range []string{"javascript:alert(1)", "JAVASCRIPT:alert(1)", " javascript:alert(1)", "java\tscript:alert(1)", "data:text/html,x", "vbscript:x", "&#106;avascript:alert(1)"} {
		noLinks(t, "classDiagram\n  class A\n  link A \""+url+"\"", RenderOptions{})
	}
	src := "classDiagram\n  class A\n  click A call stealCookies(\"now\") \"tip\"\n  callback A \"stealCookies\""
	noLinks(t, src, RenderOptions{})
	if out, _ := Render(src, RenderOptions{}); strings.Contains(out.Data, "stealCookies") {
		t.Fatal("a callback name reached the output")
	}
	if n := mutate(t, corpusOf(t, "class"), []string{"\"", "`", "~", "<<", ">>", ":::", "{", "}", "\n", " : ", "link A \"",
		"style A fill:", "note \"", "namespace N {\n", "classDef c ", "()--", "\"1\" "}, 3, 3000, RenderOptions{}, false); !testing.Short() && n <= 300 {
		t.Fatalf("%d mutated class sources rendered, want more than 300", n)
	}
}

func TestEROutputIsInert(t *testing.T) {
	for _, src := range corpusOf(t, "er") {
		inertTry(t, src, RenderOptions{}, false)
	}
	hostile(t, erTemplates, 450, RenderOptions{}, false)
	out, err := Render("erDiagram\n  \"<img src=x onerror=alert(1)>\" {\n    `<script>` `a&b` PK \"</text><script>alert(1)</script>\"\n  }", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "<script") ||
		!strings.Contains(out.Data, "a&amp;b") || !strings.Contains(out.Data, `data-id="&lt;img src=x onerror=alert(1)&gt;"`) {
		t.Fatalf("ER text is not escaped: %v\n%s", err, out.Data)
	}
	hostileOptions(t, "erDiagram\n  accTitle: t\n  A ||--o{ B : x\n  A { int id }")
	mutate(t, corpusOf(t, "er"), []string{"\"", "`", "~", "{", "}", "[", "]", "\n", ":::", " : ", "||--o{", "style A fill:",
		"classDef c ", "subgraph ", "end\n"}, 3, 4000, RenderOptions{}, false)
	out, err = Render("erDiagram\n  \"javascript:alert(1)\":::internal-link", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, `<a class="internal-link" data-href="javascript:alert(1)">`) ||
		len(out.Links) != 1 || out.Links[0] != (LinkInfo{ID: "javascript:alert(1)", Href: "javascript:alert(1)", Internal: true}) {
		t.Fatalf("internal link: %v %v\n%s", err, out.Links, out.Data)
	}
}

func TestGanttOutputIsInert(t *testing.T) {
	opt := RenderOptions{Now: time.Date(2014, 1, 16, 12, 0, 0, 0, time.Local)}
	rendered := 0
	for _, src := range corpusOf(t, "gantt") {
		if inertTry(t, src, opt, true) {
			rendered++
		}
	}
	if rendered < len(corpusOf(t, "gantt")) {
		t.Fatalf("%d of the gantt corpus rendered", rendered)
	}
	hostile(t, ganttTemplates, 500, opt, true)
	for _, payload := range PAYLOADS {
		o := opt
		o.IDPrefix, o.FontFamily = types.Ptr(payload), payload
		if !inertTry(t, ganttHead+"  accTitle: t\n"+ganttTasks, o, true) {
			t.Fatal("hostile id prefix and font: did not render")
		}
	}
	// Hostile times: the edges of the JavaScript Date range and far past them.
	for _, now := range []time.Time{
		time.UnixMilli(8.64e15), time.UnixMilli(-8.64e15), time.UnixMilli(8.64e15 + 1), time.UnixMilli(-8.64e15 - 1),
		time.Unix(0, 0), time.Unix(math.MaxInt64/4, 0), time.Unix(math.MinInt64/4, 0),
	} {
		o := RenderOptions{Now: now}
		inertTry(t, ganttHead+ganttTasks, o, true)
		inertTry(t, "gantt\n  dateFormat HH:mm\n  A :a, 17:49, 2m\n  B :after nothing, 3m\n  C :until nothing\n", o, true)
		inertTry(t, "gantt\n  A :3d\n", o, true)
	}
	mutate(t, corpusOf(t, "gantt"), []string{"\"", ":", ",", ";", "#", "\n", "%", " :a, 2014-01-01, 3d\n", "click a1 href \"",
		"click a1 call ", "todayMarker ", "axisFormat ", "dateFormat ", "excludes ", "section ", "title ", "after ", "until ",
		"milestone, ", "vert, ", "crit, ", "99999999d", "0001-01-01", "275760-09-13", "-1", "1e400",
		"tickInterval 1millisecond\n", "topAxis\n", "x", "X"}, 3, 4000, opt, true)
	for _, url := range []string{"javascript:alert(1)", "JaVaScRiPt:alert(1)", " javascript:alert(1)", "data:text/html,x", "vbscript:x"} {
		noLinks(t, ganttHead+ganttTasks+"  click a1 href \""+url+"\"\n", opt)
	}
}

func TestMindmapOutputIsInert(t *testing.T) {
	// A host with an icon for every name, so that each name a diagram gives reaches the output.
	opt := RenderOptions{Icons: func(string) string { return `<path d="M0,0"/>` }}
	rendered := 0
	for _, src := range corpusOf(t, "mindmap") {
		if inertTry(t, src, opt, false) {
			rendered++
		}
	}
	if rendered < len(corpusOf(t, "mindmap")) {
		t.Fatalf("%d of the mindmap corpus rendered", rendered)
	}
	hostile(t, mindmapTemplates, 300, opt, false)
	out, err := Render("mindmap\n  a<b>\"&[<i onload=x>\"&]\n  ::icon(x\" onload=\"y)\n  :::a\"b <c> d=e", opt)
	if err != nil || !strings.Contains(out.Data, `data-id="a&lt;b&gt;&quot;&amp;"`) ||
		!strings.Contains(out.Data, `data-icon="x&quot; onload=&quot;y"`) ||
		!strings.Contains(out.Data, `class="pele-node pele-shape-rect pele-root a b c d e"`) || strings.Contains(out.Data, "<i") {
		t.Fatalf("mindmap text, ids, icons and classes are not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		o := opt
		o.IDPrefix, o.FontFamily = types.Ptr(payload), payload
		if !inertTry(t, "mindmap\n  root\n    a\n    ::icon(x)", o, false) {
			t.Fatal("hostile id prefix and font: did not render")
		}
	}
	mutate(t, corpusOf(t, "mindmap"), []string{"\"", "[", "]", "(", ")", "((", "))", "{{", "}}", "\n", "\n    ", ":::",
		"::icon(", "\"`", "`\"", "%%"}, 3, 4000, opt, false)
}

func TestPieOutputIsInert(t *testing.T) {
	for _, src := range corpusOf(t, "pie") {
		inertTry(t, src, RenderOptions{}, false)
	}
	rendered := 0
	for _, p := range PAYLOADS {
		for _, src := range []string{
			"pie title " + p + "\n \"a\" : 1",
			"pie\n \"" + p + "\" : 1\n \"b\" : 2",
			"pie\n accTitle: " + p + "\n accDescr: " + p + "\n \"a\" : 1",
			"pie\n accDescr {\n " + p + "\n }\n \"a\" : 1",
			"---\ntitle: \"" + p + "\"\nconfig:\n  pie:\n    legendPosition: \"" + p + "\"\n    textPosition: \"" + p + "\"\n---\npie\n \"a\" : 1",
		} {
			if inertTry(t, src, RenderOptions{}, false) {
				rendered++
			}
		}
	}
	if rendered <= 60 {
		t.Fatalf("%d hostile pie sources rendered, want more than 60", rendered)
	}
	mutate(t, corpusOf(t, "pie"), []string{"\"", ":", "\n", "title ", "showData"}, 5, 2000, RenderOptions{}, false)
}

// TestInvalidUTF8 renders sources with bytes that are not UTF-8 and wants
// valid UTF-8 out, which a browser can read.
func TestInvalidUTF8(t *testing.T) {
	for _, src := range []string{
		"flowchart LR\n  A[\"a\xffb\"] --> B",
		"sequenceDiagram\n  A->>B: \xc3\x28",
		"pie title \xed\xa0\x80\n \"a\" : 1",
	} {
		out, err := Render(src, RenderOptions{})
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if !utf8.ValidString(out.Data) {
			t.Fatalf("%q: the SVG is not valid UTF-8", src)
		}
	}
}

// TestHugeCharCodes decodes numeric character references past the int64
// range in a link, as String.fromCharCode does, and keeps the link inert.
func TestHugeCharCodes(t *testing.T) {
	for _, ref := range []string{"&#1e20;", "&#18446744073709551722;", "&#-1;", "&#1e400;", "&#106.5;", "&#65642;"} {
		inertTry(t, "flowchart LR\n  A --> B\n  click A \""+ref+"avascript:alert(1)\"", RenderOptions{}, false)
		inertTry(t, "flowchart LR\n  A --> B\n  click A \""+ref+"\"", RenderOptions{}, false)
	}
}
