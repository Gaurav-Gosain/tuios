package sketch

// Ported from pele tests/render/{sequence,state,class,er,gantt,mindmap,xychart,timeline,quadrant,journey,kanban,git,sankey,block,architecture,requirement,c4,packet,radar,treemap,info,treeview,ishikawa,cynefin,eventmodeling,venn,wardley,railroad,usecase,agentflow}-security.test.ts,
// the inert parts of tests/render/pie.test.ts and tests/render/swimlane.test.ts, and tests/support/corpus.ts at
// 8aff183be57021b533f319a29341a529be934b08. The template lists are in
// security_templates_test.go, written by scripts/security-templates.py.

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
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
	kanbanBase = "---\nconfig:\n  kanban:\n    ticketBaseUrl: 'https://example.com/browse/#TICKET#'\n---\n"
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

func TestXyChartOutputIsInert(t *testing.T) {
	rendered := 0
	for _, src := range corpusOf(t, "xychart") {
		if inertTry(t, src, RenderOptions{}, false) {
			rendered++
		}
	}
	if rendered < len(corpusOf(t, "xychart")) {
		t.Fatalf("%d of the xychart corpus rendered", rendered)
	}
	hostile(t, xychartTemplates, 250, RenderOptions{}, false)
	hostileOptions(t, "xychart\n  accTitle: t\n  line \"s\" [1, 2]")
	huge := strings.Repeat("9", 400)
	for _, chart := range []string{
		"xychart\n  line [" + huge + ", 1]",
		"xychart\n  bar [-" + huge + ", " + huge + "]\n  line [1, 2]",
		"xychart\n  y-axis " + huge + " --> -" + huge + "\n  bar [1, 2]",
		"xychart\n  y-axis 0 --> " + huge + "\n  bar [1, 2]",
		"xychart\n  x-axis " + huge + " --> " + huge + "\n  line [1, 2]\n  bar [1, 2]",
		"xychart\n  x-axis -" + huge + " --> 3\n  line [1, 2]",
		"xychart\n  y-axis 5 --> 5\n  bar [5, 5]",
		"xychart\n  y-axis 9 --> 1\n  x-axis 9 --> 1\n  bar [5, 3]\n  line [2, 8]",
		"xychart\n  bar [0, 0, 0]",
		"xychart\n  line [7]",
		"xychart\n  line [0.0000001, 0.0000002]",
		"xychart\n  bar [99999999999999999999, 1]\n  line [-99999999999999999999]",
		"xychart horizontal\n  x-axis 5 --> 5\n  bar [1]",
	} {
		if !inertTry(t, chart, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(chart))
		}
	}
	for _, value := range []string{"1e999", "-1", "0", "0.0001", "99999999", "NaN", ".inf", "-.inf", "[1]", "{a: 1}", "null"} {
		src := "---\nconfig:\n  xyChart:\n    width: " + value + "\n    height: " + value + "\n    showDataLabel: true\n---\nxychart\n  x-axis x [a, b]\n  bar \"s\" [1, 2]"
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("size %s: did not render", value)
		}
	}
	mutate(t, corpusOf(t, "xychart"), []string{"\"", "[", "]", ",", "\n", " --> ", "line ", "bar ", "x-axis ", "y-axis ", "title ",
		"-", ".", "9999999999"}, 3, 4000, RenderOptions{}, false)
	// pele checks that these leave Object.prototype alone. Go has no prototypes; they must render.
	for _, src := range []string{
		"xychart\n  x-axis \"__proto__\" [\"__proto__\", \"constructor\", \"toString\"]\n  bar \"__proto__\" [1, 2, 3]\n  line \"constructor\" [3 \"__proto__\", 2, 1]",
		"---\nconfig:\n  xyChart:\n    __proto__:\n      polluted: true\n    constructor: 1\n---\nxychart\n  line [1, 2]",
	} {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", src)
		}
	}
}

func TestTimelineOutputIsInert(t *testing.T) {
	rendered := 0
	for _, src := range corpusOf(t, "timeline") {
		if inertTry(t, src, RenderOptions{}, true) {
			rendered++
		}
	}
	if rendered < len(corpusOf(t, "timeline")) {
		t.Fatalf("%d of the timeline corpus rendered", rendered)
	}
	hostile(t, timelineTemplates, 500, RenderOptions{}, true)
	out, err := Render("timeline\n  title <script>alert(1)</script>\n  section \"><img src=x onerror=alert(1)>\n  <svg onload=alert(1)> : </text><script>x</script>", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") ||
		!strings.Contains(out.Data, "&lt;script&gt;") || !strings.Contains(out.Data, `data-id="&quot;&gt;&lt;img src=x onerror=alert(1)&gt;"`) {
		t.Fatalf("timeline markup is not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "timeline\n  accTitle: t\n  accDescr: d\n  2001 : a", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, true) {
			t.Fatal("hostile id prefix and font: did not render")
		}
	}
	count := 4000
	if testing.Short() {
		count /= 8
	}
	if n := mutate(t, corpusOf(t, "timeline"), []string{":", " : ", "\n", "title ", "section ", "accTitle: ", "accDescr {", "}", "<br>",
		" TD", "#", "%%"}, 3, 4000, RenderOptions{}, true); n <= count/10 {
		t.Fatalf("%d of %d mutated timelines rendered", n, count)
	}
	// pele checks that these leave Object.prototype alone. Go has no prototypes; they must render.
	for _, src := range []string{
		"timeline\n  section __proto__\n  __proto__ : constructor : toString\n  constructor : __proto__\n  section constructor\n  hasOwnProperty",
		"---\nconfig:\n  timeline:\n    __proto__:\n      polluted: true\n---\ntimeline\n  2001 : a",
	} {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", src)
		}
	}
}

func TestQuadrantOutputIsInert(t *testing.T) {
	rendered := 0
	for _, src := range corpusOf(t, "quadrant") {
		if inertTry(t, src, RenderOptions{}, false) {
			rendered++
		}
	}
	if rendered < len(corpusOf(t, "quadrant")) {
		t.Fatalf("%d of the quadrant corpus rendered", rendered)
	}
	hostile(t, quadrantTemplates, 250, RenderOptions{}, false)
	hostileOptions(t, "quadrantChart\n  accTitle: t\n  A: [0.5, 0.5]")
	keys := []string{"chartWidth", "chartHeight", "titlePadding", "quadrantPadding", "xAxisLabelPadding", "yAxisLabelPadding",
		"quadrantTextTopPadding", "pointTextPadding", "pointRadius"}
	for _, value := range []string{"1e999", "-1", "0", "0.0001", "99999999", "NaN", ".inf", "-.inf", "[1]", "{a: 1}", "true", "null"} {
		config := make([]string, len(keys))
		for i, key := range keys {
			config[i] = "    " + key + ": " + value
		}
		src := "---\nconfig:\n  quadrantChart:\n" + strings.Join(config, "\n") +
			"\n---\nquadrantChart\n  title t\n  x-axis a --> b\n  y-axis c --> d\n  quadrant-1 q\n  A: [0.5, 0.5]"
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("settings of %s: did not render", value)
		}
	}
	huge := strings.Repeat("9", 400)
	for _, src := range []string{
		"quadrantChart\n  A: [0.5, 0.5] radius: " + huge + ", stroke-width: " + huge + "px",
		"quadrantChart\n  A: [0x5, 0e9]\n  B: [0,5, 0 5]\n  C: [055, 0b1]",
	} {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	mutate(t, corpusOf(t, "quadrant"), []string{"\"", "`", ":", ":::", ": [", "]", "\n", ", ", "classDef ", "color: #", "radius: ",
		"-->"}, 3, 4000, RenderOptions{}, false)
	// pele checks that class names like __proto__ leave Object.prototype alone and find no
	// class that was not defined.
	out, err := Render("quadrantChart\n  __proto__:::__proto__: [0.5, 0.5]\n  constructor:::constructor: [0.1, 0.1]\n  toString:::toString: [0.2, 0.2]\n"+
		"  classDef constructor radius: 3\n  classDef hasOwnProperty color: #f00", RenderOptions{})
	if err != nil || strings.Count(out.Data, "<circle") != 3 || strings.Count(out.Data, ` r="3"`) != 1 {
		t.Fatalf("classes named after Object properties: %v\n%s", err, out.Data)
	}
}

func TestJourneyOutputIsInert(t *testing.T) {
	corpus := append(corpusOf(t, "journey"),
		"journey\n  title Day\n  section Work\n    Make tea: 5: Me\n    Do work: 1: Me, Cat\n  section Home\n    Sit down: 4: Me\n",
		"journey\n  accTitle: a\n  accDescr { b }\n  First: 3\n  section S\n  One<br>two: 2: A, B, C\n")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	// A semicolon is a syntax error in a journey, so hostile text is also tried with its semicolons removed.
	noSemicolons := func(p string) string { return strings.ReplaceAll(p, ";", "") }
	hostile(t, journeyTemplates, 500, RenderOptions{}, true, noSemicolons)
	out, err := Render("journey\n  title <script>alert(1)</script>\n  section \"><img src=x onerror=alert(1)>\n  <svg onload=alert(1)>: 5: </text><script>x</script>, \"onload=\"alert(1)", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") ||
		!strings.Contains(out.Data, "&lt;script&gt;alert(1)&lt;/script&gt;") ||
		!strings.Contains(out.Data, `data-id="&quot;&gt;&lt;img src=x onerror=alert(1)&gt;"`) ||
		!strings.Contains(out.Data, "<title>&lt;/text&gt;&lt;script&gt;x&lt;/script&gt;</title>") ||
		!strings.Contains(out.Data, `data-id="&quot;onload=&quot;alert(1)"`) {
		t.Fatalf("journey markup is not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "journey\n  accTitle: t\n  accDescr: d\n  Task: 5: Me", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, true) {
			t.Fatal("hostile id prefix and font: did not render")
		}
	}
	fragments := []string{":", ": 5", ": 3: Me", ",", "\n", "title ", "section ", "accTitle: ", "accDescr {", "}", "<br>", "#", "%%"}
	for _, p := range PAYLOADS {
		if strings.Contains(p, ";") {
			fragments = append(fragments, noSemicolons(p))
		}
	}
	count := 4000
	if testing.Short() {
		count /= 8
	}
	if n := mutate(t, corpus, fragments, 3, 4000, RenderOptions{}, true); n <= count/20 {
		t.Fatalf("%d of %d mutated journeys rendered", n, count)
	}
	// pele checks that these leave Object.prototype alone. Go has no prototypes; they must render.
	out, err = Render("journey\n  section __proto__\n  constructor: 5: __proto__, constructor, toString, hasOwnProperty\n  __proto__: 1: valueOf\n", RenderOptions{})
	if err != nil || strings.Count(out.Data, `class="pele-legend-item"`) != 5 || !strings.Contains(out.Data, `data-id="__proto__"`) {
		t.Fatalf("names like __proto__: %v\n%s", err, out.Data)
	}
	if !inertTry(t, "---\nconfig:\n  journey:\n    __proto__:\n      polluted: true\n---\njourney\n  Task: 5: Me", RenderOptions{}, true) {
		t.Fatal("a config with __proto__ did not render")
	}
}

func TestKanbanOutputIsInert(t *testing.T) {
	// A host with an icon for every name, so that each name a diagram gives reaches the output.
	opt := RenderOptions{Icons: func(string) string { return `<path d="M0,0"/>` }}
	for _, src := range corpusOf(t, "kanban") {
		if !inertTry(t, src, opt, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, kanbanTemplates, 400, opt, false)
	for _, payload := range PAYLOADS {
		for _, base := range []string{payload, payload + "#TICKET#", "#TICKET#"} {
			config := Config{"kanban": map[string]any{"ticketBaseUrl": base}}
			for _, ticket := range []string{"T-1", "javascript:alert(1)", " JaVaScRiPt:alert(1)", "data:text/html,x", "$&", "$'"} {
				o := opt
				o.Config = config
				out, err := Render("kanban\n  todo\n    a@{ ticket: \""+ticket+"\" }", o)
				if err != nil {
					t.Fatalf("%q %q: %v", base, ticket, err)
				}
				if problem := inertProblem(out.Data); problem != "" {
					t.Fatalf("%q %q: output is not inert: %s", base, ticket, problem)
				}
				for _, link := range out.Links {
					if regexp.MustCompile(`(?i)^\s*(?:javascript|vbscript|data)\s*:`).MatchString(link.Href) {
						t.Fatalf("%q %q: links to %q", base, ticket, link.Href)
					}
				}
			}
		}
	}
	out, err := Render(kanbanBase+"kanban\n  a<b>\"&[<i onload=x>\"&]\n  :::a\"b <c> d=e\n    c@{ ticket: '\"><x>&', assigned: 'x < y \"&' }", opt)
	if err != nil || !strings.Contains(out.Data, `data-id="a&lt;b&gt;&quot;&amp;"`) ||
		!strings.Contains(out.Data, `class="pele-cluster pele-column a b c d e"`) ||
		!strings.Contains(out.Data, `>x &lt; y &quot;&amp;</text>`) ||
		strings.Contains(out.Data, "<i") || strings.Contains(out.Data, "<x>") ||
		len(out.Links) == 0 || out.Links[0].Href != "https://example.com/browse/%22%3E%3Cx%3E&" {
		t.Fatalf("kanban text, ids, metadata and classes are not escaped: %v %v\n%s", err, out.Links, out.Data)
	}
	for _, payload := range PAYLOADS {
		o := opt
		o.IDPrefix, o.FontFamily = types.Ptr(payload), payload
		if !inertTry(t, "kanban\n  todo\n    a@{ icon: x, ticket: y }", o, false) {
			t.Fatal("hostile id prefix and font: did not render")
		}
	}
	mutate(t, corpusOf(t, "kanban"), []string{"\"", "[", "]", "(", ")", "\n", "\n    ", ":::", "::icon(", "@{ ", " }", "@{ ticket: \"",
		"@{ label: \"", "assigned: ", "priority: High", ", "}, 3, 4000, opt, false)
	// pele checks that these leave Object.prototype alone. Go has no prototypes; they must render.
	for _, src := range []string{
		"kanban\n  __proto__\n    constructor@{ __proto__: [polluted], constructor: 1, label: x }\n    :::__proto__\n    toString[hasOwnProperty]",
		"kanban\n  todo\n    a@{\n      __proto__:\n        polluted: 1\n    }",
	} {
		if !inertTry(t, src, opt, false) {
			t.Fatalf("%q did not render", src)
		}
	}
}

// gitStrip is p.replace(/["\\\n]/g, ”), for a commit id in double quotes.
func gitStrip(p string) string {
	return strings.NewReplacer(`"`, "", `\`, "", "\n", "").Replace(p)
}

func TestGitOutputIsInert(t *testing.T) {
	// A string in double quotes cannot hold a double quote or a line break, so the payload is tried
	// as written, in single quotes, and with its quotes escaped.
	quoted := func(p string) []string {
		return []string{`"` + p + `"`, `'` + p + `'`, `"` + strings.NewReplacer(`"`, `\"`, `\`, `\\`).Replace(p) + `"`}
	}
	rendered, turn := 0, 0
	for _, payload := range PAYLOADS {
		forms := quoted(payload)
		for _, template := range gitTemplates {
			if inertTry(t, template(forms[turn%len(forms)], payload), RenderOptions{}, false) {
				rendered++
			}
			turn++
		}
	}
	if rendered <= 300 {
		t.Fatalf("%d hostile sources rendered, want more than 300", rendered)
	}
	out, err := Render("gitGraph\n commit id: \"<script>alert(1)</script>\" tag: \"\\\"><img src=x onerror=alert(1)>\"\n branch \"</text><script>\"\n commit", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") || !strings.Contains(out.Data, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("git markup is not escaped: %v\n%s", err, out.Data)
	}
	corpus := corpusOf(t, "git")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	mutate(t, corpus, []string{"\"", "'", "\n", "commit ", "branch ", "merge ", "id: \"", "tag: \"", "title ", "TB:", "cherry-pick id: \""}, 11, 2000, RenderOptions{}, false)
	hostileOptions(t, "gitGraph\n accTitle: t\n accDescr: d\n commit tag: \"v1\"\n branch b\n commit\n checkout main\n merge b")
}

const sankeyBody = "a,b,2\nb,c,1\n"

func sankeyQuoted(p string) string { return `"` + strings.ReplaceAll(p, `"`, `""`) + `"` }

func TestSankeyOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "sankey")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, sankeyTemplates, 450, RenderOptions{}, false)
	out, err := Render("sankey\n<script>alert(1)</script>,\"<img src=x onerror=alert(1)>\",1\n", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") ||
		!strings.Contains(out.Data, `data-id="&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Fatalf("sankey markup is not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		sankey := map[string]any{"prefix": payload, "suffix": payload, "linkColor": payload, "nodeAlignment": payload, "labelStyle": payload,
			"nodeColors": map[string]any{"a": payload, "b": "red;" + payload}, "width": payload, "height": payload}
		if !inertTry(t, "sankey\n"+sankeyBody, RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload, Config: Config{"sankey": sankey}}, false) {
			t.Fatalf("hostile sankey options %q: did not render", payload)
		}
	}
	for _, value := range []float64{0, -1, 1e-9, 1e9, math.Inf(1), math.Inf(-1), math.NaN()} {
		sankey := map[string]any{"width": value, "height": value, "nodeWidth": value, "nodePadding": value}
		if !inertTry(t, "sankey\n"+sankeyBody, RenderOptions{Config: Config{"sankey": sankey}}, false) {
			t.Fatalf("sankey config value %v: did not render", value)
		}
		size := 16.0
		switch {
		case value == 0:
			size = 1
		case math.Abs(value) <= 1e6 && !math.IsNaN(value) && value != 0:
			size = math.Abs(value)
		}
		pad := 8.0
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			pad = math.Mod(math.Abs(value), 100)
		}
		if !inertTry(t, "sankey\n"+sankeyBody, RenderOptions{FontSize: &size, Padding: &pad}, false) {
			t.Fatalf("sankey font size %v and padding %v: did not render", size, pad)
		}
	}
	count := 3000
	if testing.Short() {
		count /= 8
	}
	if n := mutate(t, corpus, []string{"\"", "\"\"", ",", "\n", "a,b,1", "1e308", "-1", "NaN", "sankey\n", "linkColor: ", "prefix: "}, 3, 3000, RenderOptions{}, false); n <= count/8 {
		t.Fatalf("%d of %d mutated sankey diagrams rendered", n, count)
	}
	// pele checks that these leave Object.prototype alone. Go has no prototypes; it must render.
	if !inertTry(t, "---\nconfig:\n  sankey:\n    nodeColors:\n      __proto__: red\n      constructor: blue\n---\nsankey\n__proto__,constructor,1\nconstructor,toString,2\nhasOwnProperty,__proto__,3\n", RenderOptions{}, false) {
		t.Fatal("names like __proto__ did not render")
	}
}

func TestBlockOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "block")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	rendered := 0
	var never []string
	for _, template := range blockTemplates {
		before := rendered
		for _, payload := range PAYLOADS {
			if inertTry(t, template(payload), RenderOptions{}, true) {
				rendered++
			}
		}
		if rendered == before {
			never = append(never, template("#"))
		}
	}
	if rendered <= 700 {
		t.Fatalf("%d hostile sources rendered, want more than 700", rendered)
	}
	// These positions take only digits or direction words, or hold text the grammar always rejects.
	want := []string{
		"block\n  a<[\"x\"]>(#)",
		"block\n  a[\"`#`\"]",
		"block\n  block:g:#\n    a\n  end",
		"block\n  a:# b",
		"block\n  a space:# b",
		"block\n  a\n  accDescr {\n #\n }",
		"block\n  accTitle: #\n  a",
	}
	if !slices.Equal(never, want) {
		t.Fatalf("templates that never rendered: %q, want %q", never, want)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "block\n  a space b\n  a -- \"x\" --> b", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, true) {
			t.Fatalf("hostile block options %q: did not render", payload)
		}
	}
	count := 4000
	if testing.Short() {
		count /= 8
	}
	if n := mutate(t, corpus, []string{"\"", "[\"", "\"]", "<[\"", "\"]>(right)", "\n", ":", " --> ", " -- \"", "\" --> ", "style a fill:",
		"classDef a ", "class a ", "block:", "end", "space:9", "columns 2"}, 3, 4000, RenderOptions{}, true); n <= count/20 {
		t.Fatalf("%d of %d mutated block diagrams rendered", n, count)
	}
	// Coordinates stay finite when blocks have no size.
	zero := "---\nconfig:\n  block:\n    padding: 0\n---\n"
	for _, body := range []string{
		"block\n  columns 1\n  block:g\n    classDef x fill:red\n  end\n  a\n  g --> a\n  a --> g\n  g --> g",
		"block\n  block:g\n    space\n  end\n  block:h\n    space\n  end\n  g --> h\n  h -- \"x\" --> g",
		"block\n  columns 1\n  block:g\n    classDef x fill:red\n  end\n  block:h\n    classDef x fill:red\n  end\n  g --> h",
		"block\n  space\n  block:g\n    space:0\n  end\n  g --> g",
	} {
		if !inertTry(t, zero+body, RenderOptions{}, true) || !inertTry(t, body, RenderOptions{}, true) {
			t.Fatalf("%q did not render", body)
		}
	}
	out, err := Render("block\n  a[\"</text><script>alert(1)</script>\"]\n  classDef x fill:url(javascript:alert(1)),stroke:red\n  class a x", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "url(") || !strings.Contains(out.Data, "stroke:red") {
		t.Fatalf("block markup is not escaped: %v\n%s", err, out.Data)
	}
	// pele checks that these leave Object.prototype alone. Go has no prototypes; they must render.
	for _, src := range []string{
		"block\n  __proto__ constructor toString\n  classDef __proto__ fill:red\n  class constructor,__proto__ __proto__\n  style __proto__ fill:blue\n  __proto__ --> constructor",
		"block\n  class __proto__ polluted\n  style constructor fill:red\n  a",
	} {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", src)
		}
	}
}

const archHead = "architecture-beta\n"

func TestArchitectureOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "architecture")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, architectureTemplates, 300, RenderOptions{}, false)
	src := archHead + "accTitle: t\ngroup g(cloud)[G]\nservice a(server)[A] in g\nservice b \"x\"[B]\na:R -[l]-> L:b"
	for _, payload := range PAYLOADS {
		if !inertTry(t, src, RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile architecture options %q: did not render", payload)
		}
	}
	for _, value := range []string{"0", "-1", "1e308", ".inf", ".nan", "1e-9", "99999999", "[1]", "{a: 1}", "null", "true"} {
		src := "---\nconfig:\n  architecture:\n    iconSize: " + value + "\n    fontSize: " + value + "\n    padding: " + value + "\n---\n" +
			archHead + "group g[G]\nservice a \"t\"[A] in g\nservice b[B]\na:R -[l]-> L:b"
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("architecture settings of %s: did not render", value)
		}
	}
	mutate(t, corpus, []string{"\"", "[", "]", "(", ")", "\n", " in ", "{group}", ":R --> L:", "service ", "group ", "junction ", "title ", "-[", "]-"},
		3, 4000, RenderOptions{}, false)
	// Ids that name object internals are plain ids.
	out, err := Render(archHead+"group __proto__(cloud)[P]\ngroup constructor[C] in __proto__\nservice toString(server)[A] in __proto__\n"+
		"service hasOwnProperty[B] in constructor\njunction valueOf\ntoString:R -- L:hasOwnProperty\nhasOwnProperty:B -- T:valueOf\n"+
		"align row toString hasOwnProperty", RenderOptions{})
	if err != nil || strings.Count(out.Data, `class="pele-cluster"`) != 2 || strings.Count(out.Data, `class="pele-node `) != 3 ||
		!strings.Contains(out.Data, `data-id="__proto__"`) {
		t.Fatalf("ids like __proto__: %v\n%s", err, out.Data)
	}
}

const requirementBody = "requirement a {\nid: 1\n}\nelement b {\ntype: t\n}\n"

// TestRequirementOutputIsInert leaves out pele's test of a model filled in directly, which
// the parser would not allow: the model is not reachable from this package.
func TestRequirementOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "requirement")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, requirementTemplates, 550, RenderOptions{}, false)
	out, err := Render("requirementDiagram\nrequirement \"<script>alert(1)</script>\" {\ntext: \"<img src=x onerror=alert(1)>\"\n}\n", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") ||
		!strings.Contains(out.Data, `data-id="&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Fatalf("requirement markup is not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "requirementDiagram\naccTitle: t\n"+requirementBody+"a - contains -> b\n", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile requirement options %q: did not render", payload)
		}
	}
	count := 4000
	if testing.Short() {
		count /= 8
	}
	if n := mutate(t, corpus, []string{"\"", "{", "}", ":", ":::", "\n", "style a fill:", "classDef c ", "class a ", " - contains -> ", "accTitle: "},
		3, 4000, RenderOptions{}, false); n <= count/20 {
		t.Fatalf("%d of %d mutated requirement diagrams rendered", n, count)
	}
	// pele checks that this leaves Object.prototype alone. Go has no prototypes; it must render.
	if !inertTry(t, "requirementDiagram\nrequirement __proto__ {\nid: constructor\n}\nelement constructor {\ntype: toString\n}\n"+
		"__proto__ - contains -> constructor\nclassDef __proto__ fill:red\nclass __proto__,constructor __proto__,toString\n"+
		"__proto__:::constructor\nstyle __proto__ fill:red\n", RenderOptions{}, false) {
		t.Fatal("names like __proto__ did not render")
	}
}

const c4Two = "System(a, \"A\")\nSystem(b, \"B\")\n"

// TestC4OutputIsInert leaves out pele's check that rendering leaves Object.prototype alone:
// Go has no prototypes, so the same source must only render.
func TestC4OutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "c4")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, c4Templates, 700, RenderOptions{}, false)
	for _, payload := range PAYLOADS {
		if !inertTry(t, "C4Context\naccDescr: d\nSystem(a, \"A\")\nSystem(b, \"B\")\nRel(a, b, \"x\")", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile c4 options %q: did not render", payload)
		}
	}
	mutate(t, corpus, []string{"\"", "(", ")", ",", "\n", "{", "}", "$link=\"", "$bgColor=\"", "$offsetX=\"", "Rel(a, b, \"", "UpdateElementStyle(a, \""},
		3, 4000, RenderOptions{}, false)
	for _, url := range []string{"javascript:alert(1)", " JaVaScRiPt:alert(1)", "data:text/html,x", "vbscript:x", "jav&#x09;ascript:alert(1)"} {
		noLinks(t, "C4Context\nSystem(a, \"A\", $link=\""+url+"\")\nBoundary(b, \"B\", $link=\""+url+"\") {\nSystem(c, \"C\")\n}\nRel(a, c, \"x\", $link=\""+url+"\")", RenderOptions{})
	}
	out, err := Render("C4Context\nSystem(a, \"A\")\nSystem(b, \"B\")\nRel(a, b, \"x\")\n"+
		"UpdateElementStyle(a, \"red;background:url(//evil.example/x)\", \"expression(alert(1))\", \"url(#x)\")\nUpdateRelStyle(a, b, \"red}\", \"blue<\")", RenderOptions{})
	if err != nil || regexp.MustCompile(`url\(|expression|evil|red|blue`).MatchString(out.Data) {
		t.Fatalf("colours that are more than a colour: %v\n%s", err, out.Data)
	}
	if !inertTry(t, "C4Context\nSystem(__proto__, \"A\", $__proto__=\"polluted\", $constructor=\"x\")\nSystem(constructor, \"B\", $tags=\"__proto__,constructor\", $shape=\"toString\")\n"+
		"Rel(__proto__, constructor, \"x\")\nUpdateElementStyle(__proto__, $prototype=\"x\")\nUpdateRelStyle(__proto__, constructor, $__proto__=\"1\")", RenderOptions{}, false) {
		t.Fatal("names like __proto__ did not render")
	}
}

func TestPacketOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "packet")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, packetTemplates, 150, RenderOptions{}, false)
	out, err := Render("packet\n0-31: \"<b>&amp;</b> <script>x</script>\"", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, "&lt;b&gt;&amp;&lt;/b&gt; &lt;script&gt;x&lt;/script&gt;") {
		t.Fatalf("packet label is not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "packet\naccTitle: t\n0-31: \"a\"", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile packet options %q: did not render", payload)
		}
	}
	mutate(t, corpus, []string{"\"", "'", ":", "-", "+", "\n", "title ", "0-7: \"", "+8: \""}, 3, 4000, RenderOptions{}, false)
}

func TestRadarOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "radar")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, radarTemplates, 200, RenderOptions{}, false)
	out, err := Render("radar-beta\n axis a[\"<script>x</script>\"], b[\"\\\"q\\\" & co\"]\n curve c[\"<img src=x>\"]{1,2}", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") || !strings.Contains(out.Data, "&quot;q&quot; &amp; co") {
		t.Fatalf("radar names are not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "radar-beta\n accTitle: t\n axis a, b\n curve x{1,2}", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile radar options %q: did not render", payload)
		}
	}
	mutate(t, corpus, []string{"\"", "'", "[", "]", "{", "}", ":", ",", "\n", "title ", "axis ", "curve c{1,2}", "max 3", "graticule polygon"},
		3, 4000, RenderOptions{}, false)
}

// TestTreemapOutputIsInert leaves out pele's check that rendering leaves Object.prototype alone:
// Go has no prototypes, so the same source must only render.
func TestTreemapOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "treemap")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, treemapTemplates, 250, RenderOptions{}, false)
	out, err := Render("treemap\n\"<script>x</script>\":::bad\n  \"<img src=x onerror=alert(1)>\": 1:::bad\nclassDef bad fill:url(javascript:alert(1)),stroke:red", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "url(") ||
		!strings.Contains(out.Data, `style="rx:var(--_r);stroke:red;"`) {
		t.Fatalf("treemap names or styles are not safe: %v\n%s", err, out.Data)
	}
	out, err = Render("---\nconfig:\n  treemap:\n    valueFormat: \"<b>\"\n---\ntreemap\n\"a\": 1234", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, ">1,234<") {
		t.Fatalf("a value format wrote markup: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, "treemap\naccTitle: t\n\"a\": 1", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile treemap options %q: did not render", payload)
		}
	}
	if !inertTry(t, "treemap\n\"__proto__\":::__proto__\n  \"constructor\": 1:::constructor\nclassDef __proto__ fill:red\n"+
		"classDef constructor fill:blue\nclassDef toString polluted:1", RenderOptions{}, false) {
		t.Fatal("names like __proto__ did not render")
	}
	mutate(t, corpus, []string{"\"", "'", ":", ":::", ",", "\n", "\n  ", "title ", "\"a\": 1", "classDef c1 fill:", ":::c1"}, 3, 4000, RenderOptions{}, false)
}

func TestInfoOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "info")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, infoTemplates, 150, RenderOptions{}, false)
	hostileOptions(t, "info\naccTitle: t")
	mutate(t, append(corpus, "info\ntitle T\naccTitle: A\naccDescr: D\n", "---\ntitle: T\n---\ninfo showInfo\n"),
		[]string{"\n", "title ", "accTitle: ", "accDescr {", "}", "showInfo"}, 3, 4000, RenderOptions{}, false)
}

func stripApostrophes(p string) string { return strings.ReplaceAll(p, "'", "") }

func stripQuotes(p string) string { return strings.NewReplacer(`"`, "", `\`, "").Replace(p) }

// TestTreeviewOutputIsInert leaves out pele's check for "function" and "[object" in the output:
// Go has no prototypes, so names like __proto__ must only render.
func TestTreeviewOutputIsInert(t *testing.T) {
	hostile(t, treeviewTemplates, len(PAYLOADS)*8, RenderOptions{}, false)
	for _, key := range []string{"__proto__", "constructor", "toString", "hasOwnProperty", "valueOf"} {
		src := "---\nconfig:\n  treeView:\n    showIcons: true\n    defaultIconPack: p\n---\ntreeView-beta\n  " + key + "\n  x." + key + "\n  y icon(" + key + ")\n"
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", key)
		}
	}
	corpus := corpusOf(t, "treeview")
	rendered := 0
	for _, src := range corpus {
		if inertTry(t, src, RenderOptions{}, false) {
			rendered++
		}
	}
	rendered += mutate(t, corpus, []string{" :::", " icon(", " ## ", "\"", "'", "\n", "    ", "├── ", "/"}, 11, 2000, RenderOptions{}, false)
	if !testing.Short() && rendered <= 300 {
		t.Fatalf("%d mutated treeView sources rendered, want more than 300", rendered)
	}
}

const ishikawaHead = "ishikawa-beta\n"

// TestIshikawaOutputIsInert leaves out pele's prototype check: Go has no prototypes, so the same
// source must only render.
func TestIshikawaOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "ishikawa")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, ishikawaTemplates, 450, RenderOptions{}, false)
	out, err := Render(ishikawaHead+"  <img src=x onerror=alert(1)>\n    <script>a&b</script>\n      </text></g></svg><script>alert(1)</script>\n        \" onload=\"alert(1)\n", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, `onload="alert`) ||
		!strings.Contains(out.Data, `data-id="&lt;img src=x onerror=alert(1)&gt;"`) || !strings.Contains(out.Data, `data-id="&lt;script&gt;a&amp;b&lt;/script&gt;"`) ||
		!strings.Contains(out.Data, "&quot; onload=&quot;alert(1)") {
		t.Fatalf("ishikawa text is not escaped: %v\n%s", err, out.Data)
	}
	hostileOptions(t, ishikawaHead+"  Effect\n    Category\n      Cause")
	if !inertTry(t, ishikawaHead+"  __proto__\n    constructor\n      toString\n        hasOwnProperty\n    __proto__\n", RenderOptions{}, false) {
		t.Fatal("names like __proto__ did not render")
	}
	mutate(t, corpus, []string{"\n", "\n  ", "\n      ", "\t", " ", "%%", "<br>", "#35;", "ishikawa"}, 3, 4000, RenderOptions{}, false)
}

func cynefinQuoted(p string) string {
	return strings.NewReplacer(`"`, "", `\`, "", "\n", "").Replace(p)
}

func cynefinSingle(p string) string {
	return strings.NewReplacer(`'`, "", `\`, "", "\n", "").Replace(p)
}

func TestCynefinOutputIsInert(t *testing.T) {
	hostile(t, cynefinTemplates, len(PAYLOADS)*8, RenderOptions{}, false)
	for _, body := range []string{"width: 1\n    height: 1", "width: 10000\n    height: 10000", "seed: 1e300", "seed: -7", "boundaryAmplitude: 50", "boundaryAmplitude: -1"} {
		src := "---\nconfig:\n  cynefin:\n    " + body + "\n---\ncynefin-beta\ncomplex\n\"a\"\ncomplex --> clear\n"
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", src)
		}
	}
	corpus := corpusOf(t, "cynefin")
	rendered := 0
	for _, src := range corpus {
		if inertTry(t, src, RenderOptions{}, false) {
			rendered++
		}
	}
	rendered += mutate(t, corpus, []string{"\"", "'", "\n", " --> ", " : ", "complex", "confusion", "title "}, 13, 2000, RenderOptions{}, false)
	if !testing.Short() && rendered <= 100 {
		t.Fatalf("%d mutated cynefin sources rendered, want more than 100", rendered)
	}
}

func emLine(p string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(p) }

// TestEventmodelingOutputIsInert leaves out pele's check for "function" and "[object" in the output:
// Go has no prototypes, so names like __proto__ must only render.
func TestEventmodelingOutputIsInert(t *testing.T) {
	hostile(t, eventmodelingTemplates, len(PAYLOADS)*8, RenderOptions{}, false)
	for _, key := range []string{"__proto__", "constructor", "toString", "hasOwnProperty", "valueOf"} {
		src := "eventmodeling\ntf 01 cmd " + key + "." + key + " [[" + key + "]]\ntf 02 evt " + key + "\ndata " + key + " {\n x\n}\nentity " + key + "\ngwt 01 given evt " + key + " then evt " + key + "\n"
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", key)
		}
	}
	corpus := corpusOf(t, "eventmodeling")
	rendered := 0
	for _, src := range corpus {
		if inertTry(t, src, RenderOptions{}, false) {
			rendered++
		}
	}
	rendered += mutate(t, corpus, []string{"\"", "'", "\n", "{", "}", " ->> 01", "[[", "]]", "tf 09 evt Z", "note 01 {\n", "\n}\n"}, 19, 2000, RenderOptions{}, false)
	if !testing.Short() && rendered <= 100 {
		t.Fatalf("%d mutated event models rendered, want more than 100", rendered)
	}
}

const vennHead = "venn-beta\n"

// TestVennOutputIsInert leaves out pele's prototype check: Go has no prototypes, so the same
// source must only render.
func TestVennOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "venn")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, vennTemplates, 400, RenderOptions{}, false)
	out, err := Render(vennHead+"title <img src=x onerror=alert(1)>\nset \"<script>a&b</script>\"\n  text \"</text><script>alert(1)</script>\"\nset B[<svg onload=alert(1)>]\n"+
		"style B fill:\"url(javascript:alert(1))\", stroke:\"red' onload='alert(1)\"", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<svg onload") ||
		strings.Contains(out.Data, "javascript:") || !strings.Contains(out.Data, `data-id="&lt;script&gt;a&amp;b&lt;/script&gt;"`) ||
		!strings.Contains(out.Data, "&lt;/text&gt;&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("venn text is not escaped: %v\n%s", err, out.Data)
	}
	hostileOptions(t, vennHead+"set A\nset B\nunion A,B[\"x\"]")
	if !inertTry(t, vennHead+"set __proto__\nset constructor\nunion __proto__,constructor[\"toString\"]\n  text hasOwnProperty[\"__proto__\"]\n"+
		"style __proto__ __proto__:red, constructor:blue\nstyle hasOwnProperty color:red", RenderOptions{}, false) {
		t.Fatal("names like __proto__ did not render")
	}
	mutate(t, corpus, []string{"\"", "[", "]", "[\"", "\"]", "\n", "\n  ", ",", ":", "set ", "union ", "text ", "style ", "title ", "fill:", "%%", ":5", ":-1", ":1e9"},
		3, 4000, RenderOptions{}, false)
}

func TestWardleyOutputIsInert(t *testing.T) {
	hostile(t, wardleyTemplates, len(PAYLOADS)*12, RenderOptions{}, false)
	nines := strings.Repeat("9", 400)
	for _, src := range []string{
		"wardley-beta\nsize [" + nines + ", " + nines + "]\ncomponent A [0.5, 0.5]",
		"wardley-beta\nsize [0, 0]\ncomponent A [0.5, 0.5]",
		"wardley-beta\ncomponent A [0.5, 0.5] label [-" + nines + ", " + nines + "]",
		"wardley-beta\nannotation " + nines + ",[0.5, 0.5] \"big\"\nannotations [1, 1]",
		"wardley-beta\nevolution A@" + nines + ".0 -> B@0.0\ncomponent A [0.0, 1.0]",
		"wardley-beta\ncomponent A [0.5, 0.5]\ncomponent B [0.5, 0.5]\nA -> B\nA +<> B\nevolve A 0.5",
		"wardley-beta\ncomponent A [0.5, 0.5]\npipeline A {\n component B [0.5]\n component C [0.5]\n}\nevolve A 0.50",
		"---\nconfig:\n  wardley-beta:\n    width: 1\n    height: 100000\n---\nwardley-beta\ncomponent A [0.5, 0.5]",
	} {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	corpus := corpusOf(t, "wardley")
	rendered := 0
	for _, src := range corpus {
		if inertTry(t, src, RenderOptions{}, false) {
			rendered++
		}
	}
	rendered += mutate(t, corpus, []string{"\"", "'", "\n", " -> ", "; ", "+'", "component ", " [0.5, 0.5]", "note ", "(buy)"}, 17, 2000, RenderOptions{}, false)
	if !testing.Short() && rendered <= 100 {
		t.Fatalf("%d mutated wardley maps rendered, want more than 100", rendered)
	}
}

func railroadQuoted(p string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) }

func railroadBare(p string) string { return strings.ReplaceAll(p, `"`, "'") }

func railroadNoSpecial(p string) string { return strings.NewReplacer("?", "", ";", "").Replace(p) }

func railroadSingle(p string) string { return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(p) }

// TestRailroadOutputIsInert leaves out pele's prototype check, as the other types do, and
// mutates the corpus of all four notations together where pele mutates each with its own
// generated grammars.
func TestRailroadOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "railroad")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, railroadTemplates, 700, RenderOptions{}, false)
	out, err := Render("railroad-beta\ntitle <b onclick=alert(1)>T</b>\nr = sequence(terminal(\"<img src=x onerror=alert(1)>\"), nonterminal(\"\\\"><script>alert(1)</script>\"), "+
		"special(\"</text></g></svg><script>a&b</script>\")) ;", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "onclick") ||
		!strings.Contains(out.Data, "&lt;img src=x onerror=alert(1)&gt;") || !strings.Contains(out.Data, `data-id="&quot;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`) ||
		!strings.Contains(out.Data, "a&amp;b") {
		t.Fatalf("railroad text is not escaped: %v\n%s", err, out.Data)
	}
	src := "railroad-ebnf-beta\naccTitle: t\naccDescr: d\nr = a \"b\" ? c ? - d* ;"
	for _, payload := range PAYLOADS {
		for _, opt := range []RenderOptions{
			{IDPrefix: types.Ptr(payload), FontFamily: payload},
			{Config: Config{"railroad": map[string]any{"arcRadius": payload, "padding": payload, "fontSize": payload, "strokeWidth": payload, "markerRadius": payload, "showMarkers": payload}}},
			{Config: Config{"railroad": payload}},
		} {
			if !inertTry(t, src, opt, false) {
				t.Fatalf("hostile railroad options %q: did not render", payload)
			}
		}
	}
	for _, src := range []string{
		"railroad-ebnf-beta\n__proto__ = constructor | toString hasOwnProperty ;\nconstructor = \"__proto__\" ;\n__proto__ = prototype ;",
		"railroad-beta\nr = sequence(nonterminal(\"__proto__\"), terminal(\"constructor\"), special(\"__proto__\")) ;",
	} {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", src)
		}
	}
	mutate(t, corpus, []string{"\n", " ", "(", ")", "\"", "'", ";", "|", "/", "[", "]", "{", "}", "?", "*", "+", "-", "=", "::=", "<-", "!", "&", ".",
		"terminal(\"a\")", "sequence(", "choice(", "optional(", "zeroOrMore(", "2*3", "%x41", "(* c *)", "/* c */", "# c\n", "title ", "accDescr {"}, 3, 2400, RenderOptions{}, false)
}

var reLane = regexp.MustCompile(`<g class="pele-cluster pele-lane[^"]*" data-id="([^"]*)"><rect`)

func TestSwimlaneOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "swimlane")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, swimlaneTemplates, 40, RenderOptions{}, true)
	hostileOptions(t, "swimlane-beta LR\n  accTitle: t\n  subgraph L[\"Lane\"]\n    a --> b\n  end\n  subgraph M\n    c\n  end\n  b -- x --> c")
	mutate(t, corpus, []string{"\"", "[", "]", "\n", "subgraph L\n", "end\n", " --> ", " -- x --> ", "direction LR\n", ":::", "style a fill:", "click a \"", "swimlane-beta LR\n"},
		3, 4000, RenderOptions{}, true)
	// Lane names that are object keys in JavaScript are plain names.
	for _, name := range []string{"__proto__", "constructor", "toString", "hasOwnProperty"} {
		out, err := Render("swimlane-beta TD\n  subgraph "+name+"\n    a --> b\n  end\n  subgraph other\n    c\n  end\n  b --> c", RenderOptions{})
		if err != nil || len(reLane.FindAllString(out.Data, -1)) != 2 {
			t.Fatalf("lane %s: %v\n%s", name, err, out.Data)
		}
	}
}

const usecaseHead = "usecase-beta\n"

// TestUsecaseOutputIsInert leaves out pele's check that rendering leaves Object.prototype alone:
// Go has no prototypes, so the same sources must only render.
func TestUsecaseOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "usecase")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, usecaseTemplates, 600, RenderOptions{}, false)
	out, err := Render(usecaseHead+"actor \"<img src=x onerror=alert(1)>\" <<</text><script>alert(1)</script >>\n'<script>a&b</script>'\n"+
		"json P@{\"<b>\": \"</text><script>alert(1)</script>\"}\nnote for P2 \"<svg onload=alert(1)>\"\nP2", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<svg onload") ||
		!strings.Contains(out.Data, "&lt;script&gt;a&amp;b&lt;/script&gt;") || !strings.Contains(out.Data, `data-id="_img_src_x_onerror_alert_1__"`) ||
		!strings.Contains(out.Data, ">&lt;b&gt;</text>") || !strings.Contains(out.Data, "«&lt;/text&gt;&lt;script&gt;alert(1)&lt;/script»") {
		t.Fatalf("use case markup is not escaped: %v\n%s", err, out.Data)
	}
	out, err = Render(usecaseHead+"actor A@{ icon: '\"><script>alert(1)</script>' }", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<script") || !strings.Contains(out.Data, `data-icon="&quot;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Fatalf("an icon name is markup: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, usecaseHead+"accTitle: t\nactor A\nA -- \"l\" --> B\njson P@{\"a\":1}", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, false) {
			t.Fatalf("hostile use case options %q: did not render", payload)
		}
	}
	mutate(t, corpus, []string{"\"", "'", "`", "\"`", "`\"", "{", "}", "[", "]", "(", ")", "\n", ":::", "<<", ">>", "@{", " --> ", " -- ", "style A fill:",
		"classDef c ", "systemBoundary ", "end\n", "json P@", "note for A "}, 3, 4000, RenderOptions{}, false)
	for _, src := range []string{
		usecaseHead + "actor __proto__\n__proto__ constructor@--> toString\nconstructor@{ animate: true }\nnote for toString \"__proto__\"",
		usecaseHead + "A\nclassDef __proto__ fill:red\nclass A __proto__\nstyle A fill:red\nA@{ business: true }",
		usecaseHead + "systemBoundary __proto__[constructor]\n  hasOwnProperty\n  actor valueOf\nend\n__proto__@{ type: package }",
		usecaseHead + "json __proto__@{\"__proto__\": {\"polluted\": true}, \"constructor\": {\"prototype\": {\"polluted\": true}}}\nA --> __proto__",
	} {
		if !inertTry(t, src, RenderOptions{}, false) {
			t.Fatalf("%q did not render", src)
		}
	}
	out, err = Render(usecaseHead+"json P@{\"__proto__\": {\"x\": 1}, \"constructor\": 2}", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, ">__proto__.x</text>") || !strings.Contains(out.Data, ">constructor</text>") {
		t.Fatalf("a JSON key named __proto__: %v\n%s", err, out.Data)
	}
}

const agentflowHead = "agentflow-beta TB\n"

// TestAgentflowOutputIsInert leaves out pele's check that rendering leaves Object.prototype alone:
// Go has no prototypes, so the same sources must only render.
func TestAgentflowOutputIsInert(t *testing.T) {
	corpus := corpusOf(t, "agentflow")
	for _, src := range corpus {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", trimSrc(src))
		}
	}
	hostile(t, agentflowTemplates, 900, RenderOptions{}, true)
	out, err := Render(agentflowHead+"  a[\"<img src=x onerror=alert(1)>\"] -- \"</text><script>alert(1)</script>\" --> b[\"a&b\"]\n  flow f[\"<svg onload=alert(1)>\"]\n   c\n  end", RenderOptions{})
	if err != nil || strings.Contains(out.Data, "<img") || strings.Contains(out.Data, "<script") || strings.Contains(out.Data, "<svg onload") || !strings.Contains(out.Data, "a&amp;b") {
		t.Fatalf("agentflow markup is not escaped: %v\n%s", err, out.Data)
	}
	out, err = Render(agentflowHead+"  flow q&r\n  a\n  end\n  connector 'x&y'", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, `data-id="q&amp;r"`) || !strings.Contains(out.Data, `data-id="'x&amp;y'"`) {
		t.Fatalf("agentflow ids are not escaped: %v\n%s", err, out.Data)
	}
	for _, payload := range PAYLOADS {
		if !inertTry(t, agentflowHead+"  accTitle: t\n  flow f[\"F\"]\n  a --> b\n  end", RenderOptions{IDPrefix: types.Ptr(payload), FontFamily: payload}, true) {
			t.Fatalf("hostile agentflow options %q: did not render", payload)
		}
	}
	mutate(t, corpus, []string{"\"", "`", "{", "}", "[", "]", "(", ")", "|", "\n", ":::", "-->", "--x", "-.-", " -- ", "@{ ", "label: ",
		"shape: ", "view: collapsed", "style a fill:", "classDef c ", "click a href \"", "flow ", "connector ", "global\n", "end\n", "%% "}, 3, 4000, RenderOptions{}, true)
	for _, url := range []string{"javascript:alert(1)", " JaVaScRiPt:alert(1)", "data:text/html,<script>alert(1)</script>", "vbscript:x"} {
		noLinks(t, agentflowHead+"  a\n  click a href \""+url+"\"", RenderOptions{})
	}
	out, err = Render(agentflowHead+"  a[\"javascript:alert(1)\"]:::internal-link", RenderOptions{})
	if err != nil || !strings.Contains(out.Data, `<a class="internal-link" data-href="javascript:alert(1)">`) ||
		len(out.Links) != 1 || out.Links[0] != (LinkInfo{ID: "a", Href: "javascript:alert(1)", Internal: true}) {
		t.Fatalf("an internal link to a script: %v %v\n%s", err, out.Links, out.Data)
	}
	for _, src := range []string{
		agentflowHead + "  __proto__ --> constructor --> toString\n  flow __proto__[constructor]\n  hasOwnProperty\n  end\n  connector prototype[\"__proto__\"]",
		agentflowHead + "  a\n  classDef __proto__ fill:red\n  class a __proto__\n  class __proto__ constructor\n  style __proto__ fill:red\n  a:::__proto__",
		agentflowHead + "  a@{ __proto__: 1, constructor: 2, prototype: 3 }\n  a@{\n    __proto__:\n      polluted: true\n    constructor:\n      prototype:\n        polluted: true\n  }",
		agentflowHead + "  a[|__proto__:polluted|x]\n  global\n  __proto__\n  end\n  flow f@{ __proto__: 1 }\n  __proto__\n  end",
		agentflowHead + "  a@{ shape: constructor }",
	} {
		if !inertTry(t, src, RenderOptions{}, true) {
			t.Fatalf("%q did not render", src)
		}
	}
}
