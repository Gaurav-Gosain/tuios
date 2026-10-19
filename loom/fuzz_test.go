package loom

import (
	"encoding/json"
	"strconv"
	"testing"

	"tuios.dev/tml"
)

// reader hands out fuzz bytes as small choices.
type reader struct {
	b []byte
	i int
}

func (r *reader) byte() byte {
	if r.i >= len(r.b) {
		return 0
	}
	r.i++
	return r.b[r.i-1]
}

func (r *reader) pick(n int) int { return int(r.byte()) % n }

var fuzzTypes = []string{
	"row", "col", "grid", "stack", "scroll", "split", "card", "tabs", "list", "spacer",
	"text", "heading", "badge", "stat", "gauge", "progress", "table", "kv", "code", "button",
	"bar", "line", "area", "sparkline", "histogram", "status", "input", "toggle", "slider", "tree", "modal", "toast", "pie",
}

var fuzzSizes = []string{"", "1", "3", "10", "50%", "100%", "fill", "fill:3", "0", "200"}

var fuzzTexts = []string{"", "a", "hello world", "日本語のテキスト", "👍🏽🇯🇵", "e\u0301", "\x1b[2J", "tab\there", "line\nbreak", "\u202eevil", "{state.n}", "{state.s}", "{len(state.rows)}"}

// fuzzTree builds a random tree, not necessarily one tml.Check accepts:
// loom must draw any tree.
func fuzzTree(r *reader, depth int) *tml.Node {
	typ := fuzzTypes[r.pick(len(fuzzTypes))]
	if depth > 32 {
		typ = "text"
	}
	n := &tml.Node{Type: typ}
	set := func(name string, v tml.Value) { n.SetAttr(name, v) }
	if s := fuzzSizes[r.pick(len(fuzzSizes))]; s != "" {
		set("w", tml.StringValue(s))
	}
	if s := fuzzSizes[r.pick(len(fuzzSizes))]; s != "" {
		set("h", tml.StringValue(s))
	}
	if r.pick(4) == 0 {
		set("pad", tml.StringValue(strconv.Itoa(r.pick(3))))
	}
	if r.pick(4) == 0 {
		set("gap", tml.NumberValue(float64(r.pick(3))))
	}
	if r.pick(5) == 0 {
		set("min-w", tml.StringValue(fuzzSizes[r.pick(len(fuzzSizes))]))
		set("max-w", tml.StringValue(fuzzSizes[r.pick(len(fuzzSizes))]))
	}
	if r.pick(6) == 0 {
		set("align", tml.StringValue([]string{"start", "center", "end", "stretch"}[r.pick(4)]))
		set("justify", tml.StringValue([]string{"start", "center", "end", "between", "around"}[r.pick(5)]))
	}
	if r.pick(8) == 0 {
		set("show", tml.StringValue("state.n > 2"))
	}
	if r.pick(8) == 0 {
		set("each", tml.StringValue("state.rows"))
	}
	text := func() { n.Children = append(n.Children, tml.TextNode(fuzzTexts[r.pick(len(fuzzTexts))])) }
	switch typ {
	case "row", "col", "grid", "stack", "scroll", "split", "card", "modal":
		if typ == "grid" {
			set("cols", tml.NumberValue(float64(1+r.pick(4))))
		}
		if typ == "modal" {
			set("open", tml.BoolValue(true))
		}
		if typ == "card" && r.pick(2) == 0 {
			set("fold", tml.BoolValue(true))
		}
		for range r.pick(4) {
			n.Children = append(n.Children, fuzzTree(r, depth+1))
		}
	case "tabs":
		for i := range 1 + r.pick(3) {
			tab := &tml.Node{Type: "tab"}
			tab.SetAttr("id", tml.StringValue("t"+strconv.Itoa(i)))
			tab.SetAttr("title", tml.StringValue(fuzzTexts[r.pick(len(fuzzTexts))]))
			tab.Children = []*tml.Node{fuzzTree(r, depth+1)}
			n.Children = append(n.Children, tab)
		}
	case "list":
		set("items", tml.StringValue("state.rows"))
		set("select", tml.StringValue("single"))
		n.Children = []*tml.Node{fuzzTree(r, depth+1)}
	case "table":
		set("rows", tml.StringValue("state.rows"))
		set("select", tml.StringValue("single"))
		set("sort", tml.StringValue("v:desc"))
		for _, f := range []string{"k", "v"} {
			c := &tml.Node{Type: "column"}
			c.SetAttr("field", tml.StringValue(f))
			c.SetAttr("w", tml.StringValue(fuzzSizes[r.pick(len(fuzzSizes))]))
			n.Children = append(n.Children, c)
		}
	case "kv":
		it := &tml.Node{Type: "item"}
		it.SetAttr("k", tml.StringValue(fuzzTexts[r.pick(len(fuzzTexts))]))
		it.SetAttr("v", tml.StringValue(fuzzTexts[r.pick(len(fuzzTexts))]))
		n.Children = []*tml.Node{it}
	case "stat", "gauge", "progress", "status", "input", "slider", "toggle":
		set("label", tml.StringValue(fuzzTexts[r.pick(len(fuzzTexts))]))
		set("value", tml.StringValue([]string{"{state.n}", "1e308", "-5", "x", "{state.s}"}[r.pick(5)]))
		set("state", tml.StringValue("working"))
	case "bar", "line", "area", "pie":
		set("data", tml.StringValue("state.rows"))
		set("y", tml.StringValue("v"))
		set("x", tml.StringValue("k"))
		set("label", tml.StringValue("k"))
		set("v", tml.StringValue("v"))
		// No new byte is read here: the kept corpus inputs must decode to
		// the same trees.
		set("style", tml.StringValue("donut"))
		if r.pick(2) == 0 {
			set("dir", tml.StringValue("h"))
		}
		set("axis", tml.BoolValue(r.pick(2) == 0))
	case "sparkline", "histogram":
		set("values", tml.StringValue("state.nums"))
	case "tree":
		set("items", tml.StringValue("state.rows"))
		set("label", tml.StringValue("k"))
	case "spacer":
	default:
		text()
		if r.pick(2) == 0 {
			b := &tml.Node{Type: "b"}
			b.Children = []*tml.Node{tml.TextNode(fuzzTexts[r.pick(len(fuzzTexts))])}
			n.Children = append(n.Children, b)
		}
	}
	return n
}

const fuzzState = `{"n": 3, "s": "state \u202e text", "nums": [1, 2, 1e308, -3, 0.5],
  "rows": [{"k": "a", "v": 1}, {"k": "日本", "v": 2.5}, {"k": "", "v": -1}, {"k": "x", "v": null}]}`

func fuzzDoc(r *reader) *tml.Node {
	doc := &tml.Node{Type: "tml"}
	doc.SetAttr("version", tml.NumberValue(1))
	doc.SetAttr("state", tml.Value{Kind: tml.KindObject, Raw: json.RawMessage(fuzzState)})
	for range 1 + r.pick(3) {
		doc.Children = append(doc.Children, fuzzTree(r, 0))
	}
	return doc
}

// FuzzLayout draws random trees at random sizes and depths. No tree may
// panic, every row must be exactly the surface's width as ansi.StringWidth
// counts its graphemes, and no control character may reach a cell.
func FuzzLayout(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, uint8(40), uint8(10), uint8(0))
	f.Add([]byte{1, 3, 10, 11, 12, 13, 14, 15}, uint8(1), uint8(1), uint8(2))
	f.Add([]byte{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}, uint8(120), uint8(40), uint8(3))
	f.Add([]byte{}, uint8(0), uint8(0), uint8(1))
	f.Add(rowChainSeed(31), uint8(120), uint8(40), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, w, h, depth uint8) {
		r := &reader{b: data}
		doc := fuzzDoc(r)
		s, err := New(doc, &Options{Palette: DefaultPalette(Depth(depth % 4))})
		if err != nil {
			t.Skip(err)
		}
		W, H := int(w%160), int(h%60)
		buf := s.Render(W, H)
		checkFits(t, buf, W, H)
		// The time bound, without a clock: layout measures each box a
		// bounded number of times, whatever the nesting.
		if n := countBoxes(s.root); s.stats.measures > 64*(n+1) {
			t.Fatalf("one render measured %d times for %d boxes", s.stats.measures, n)
		}
		// Keys and clicks must not panic either, and the picture after them
		// must equal a full redraw.
		for _, k := range []string{"down", "tab", "enter", "s", "right"} {
			_, _ = s.Key(k)
		}
		_, _ = s.Mouse(Mouse{X: W / 2, Y: H / 2, Button: MouseLeft})
		_, _ = s.Mouse(Mouse{X: W / 3, Y: H / 3, Button: WheelDown})
		buf = s.Render(W, H)
		checkFits(t, buf, W, H)
		sameAsFresh(t, s, W, H)
	})
}

// FuzzIncremental applies random patches, keys, clicks and resizes to the
// test-run dashboard and checks after each step that the incremental
// picture equals a full redraw. It guards the dirty tracking: a box that
// reads a path but is not marked would show stale text here.
func FuzzIncremental(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{3, 3, 3, 9, 9, 1, 2, 0, 5})
	f.Add([]byte{8, 8, 8, 6, 6, 7, 7, 4, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64 {
			data = data[:64]
		}
		s := fileSurface(t, "testdata/examples-golden/test-run.tml")
		w, h := 100, 30
		s.Render(w, h)
		r := &reader{b: data}
		for r.i < len(r.b) {
			switch r.pick(10) {
			case 0:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/passed", Value: []byte(strconv.Itoa(r.pick(1000)))}})
			case 1:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/failed", Value: []byte(strconv.Itoa(r.pick(9)))}})
			case 2:
				_ = s.Patch([]tml.Op{{Op: "add", Path: "/failures/0", Value: []byte(`{"pkg": "p", "test": "T` + strconv.Itoa(r.pick(99)) + `", "ms": ` + strconv.Itoa(r.pick(5000)) + `, "line": "x"}`)}})
			case 3:
				_ = s.Patch([]tml.Op{{Op: "remove", Path: "/failures/0"}})
			case 4:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/tab", Value: []byte([]string{`"sum"`, `"fail"`, `"log"`}[r.pick(3)])}})
			case 5:
				_, _ = s.Key([]string{"up", "down", "s", "enter", "tab", "left", "right", "pgdown", "end"}[r.pick(9)])
			case 6:
				_, _ = s.Mouse(Mouse{X: r.pick(w), Y: r.pick(h), Button: MouseLeft})
			case 7:
				_, _ = s.Mouse(Mouse{X: r.pick(w), Y: r.pick(h), Button: []MouseButton{WheelUp, WheelDown}[r.pick(2)]})
			case 8:
				w, h = 40+r.pick(120), 10+r.pick(40)
			case 9:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/byPkg/0/secs", Value: []byte(strconv.Itoa(r.pick(20)))}})
			}
			sameAsFresh(t, s, w, h)
		}
	})
}

// rowChainSeed is FuzzLayout input for one row nested depth times, around
// one text: each row reads type 0 (row), no w or h, no pad, gap, min-w,
// align, show or each, and one child.
func rowChainSeed(depth int) []byte {
	b := []byte{0} // one root child
	for range depth {
		b = append(b, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1)
	}
	// The text: type 10, no w or h, none of the options, the text "a", no b.
	return append(b, 10, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1)
}

func countBoxes(b *box) int {
	if b == nil {
		return 0
	}
	n := 1
	for _, k := range b.kids {
		n += countBoxes(k)
	}
	return n
}

// listDoc has if and each inside a list's item template, a form whose
// text reads form.x, and a button inside a box that show hides.
const listDoc = `<tml version="1"><state>{"more": true, "rows": [{"x": "a", "n": [1, 2]}, {"x": "b", "n": [3]}], "y": "hello"}</state>
<col>
  <list items="state.rows" select="single">
    <text if="state.more">{item.x}</text>
    <text each="item.n" as="k">- {k}</text>
    <badge show="state.more">{item.x}</badge>
  </list>
  <list items="state.rows"><text if="state.more">second {item.x}</text></list>
  <form id="f">
    <input name="x" label="X" bind="state.y"/>
    <text>typed: {form.x}</text>
    <toggle name="t" label="T" bind="state.more"/>
  </form>
  <col show="state.more"><button key="d" on-press="emit('d')">Delete</button></col>
</col></tml>`

// FuzzIncrementalList applies random patches, keys and clicks to listDoc
// and checks the incremental picture against a full redraw after each
// step. It reaches the rebuild of a list item (an if or each inside the
// template), form.NAME readers, and boxes that show hides.
func FuzzIncrementalList(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{0, 0, 3, 3, 1, 5, 0, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64 {
			data = data[:64]
		}
		s := mustSurface(t, listDoc)
		w, h := 40, 20
		s.Render(w, h)
		r := &reader{b: data}
		for r.i < len(r.b) {
			switch r.pick(9) {
			case 0:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/more", Value: []byte([]string{"true", "false"}[r.pick(2)])}})
			case 1:
				_ = s.Patch([]tml.Op{{Op: "add", Path: "/rows/-", Value: []byte(`{"x": "r` + strconv.Itoa(r.pick(9)) + `", "n": [` + strconv.Itoa(r.pick(9)) + `]}`)}})
			case 2:
				_ = s.Patch([]tml.Op{{Op: "remove", Path: "/rows/0"}})
			case 3:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/rows/0/x", Value: []byte(`"z` + strconv.Itoa(r.pick(99)) + `"`)}})
			case 4:
				_ = s.Patch([]tml.Op{{Op: "add", Path: "/rows/0/n/-", Value: []byte(strconv.Itoa(r.pick(99)))}})
			case 5:
				_ = s.Patch([]tml.Op{{Op: "replace", Path: "/y", Value: []byte(`"y` + strconv.Itoa(r.pick(99)) + `"`)}})
			case 6:
				_, _ = s.Key([]string{"up", "down", "enter", "tab", "d", "space"}[r.pick(6)])
			case 7:
				_, _ = s.Mouse(Mouse{X: r.pick(w), Y: r.pick(h), Button: MouseLeft})
			case 8:
				w, h = 20+r.pick(40), 8+r.pick(20)
			}
			sameAsFresh(t, s, w, h)
			assertReachable(t, s)
		}
	})
}

// assertReachable checks that every box the pointer index holds is in the
// tree: a rebuild must not leave orphans, which would hold a share of the
// expansion budget and of memory.
func assertReachable(t testing.TB, s *Surface) {
	t.Helper()
	in := map[*box]bool{}
	var walk func(b *box)
	walk = func(b *box) {
		in[b] = true
		for _, k := range b.kids {
			walk(k)
		}
	}
	walk(s.root)
	for ptr, bs := range s.byPtr {
		for _, b := range bs {
			if !in[b] {
				t.Fatalf("an orphan box of %s (%s) is in the index", ptr, b.kind)
			}
		}
	}
}
