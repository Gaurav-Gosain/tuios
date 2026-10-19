package loom

import (
	"image"
	"maps"
	"os"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/tml"
)

func mustSurface(t testing.TB, src string) *Surface {
	t.Helper()
	doc, problems, err := tml.Check([]byte(src), nil)
	if err != nil {
		t.Fatalf("%v %v", err, problems)
	}
	s, err := New(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fileSurface(t testing.TB, path string) *Surface {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return mustSurface(t, string(src))
}

// rows returns the text of a buffer, one string per row.
func rows(buf *uv.Buffer) []string {
	var out []string
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			if c := buf.CellAt(x, y); c.Width > 0 {
				b.WriteString(c.Content)
			}
		}
		out = append(out, b.String())
	}
	return out
}

func screenHas(t *testing.T, buf *uv.Buffer, want string) {
	t.Helper()
	for _, r := range rows(buf) {
		if strings.Contains(r, want) {
			return
		}
	}
	t.Fatalf("the screen has no %q:\n%s", want, strings.Join(rows(buf), "\n"))
}

// rowWith returns the row that holds s, or -1.
func rowWith(buf *uv.Buffer, s string) int {
	for i, r := range rows(buf) {
		if strings.Contains(r, s) {
			return i
		}
	}
	return -1
}

func cloneView(v *View) *View {
	return &View{Tabs: maps.Clone(v.Tabs), Sort: maps.Clone(v.Sort), Filter: maps.Clone(v.Filter), Fold: maps.Clone(v.Fold),
		Focus: v.Focus, Cursor: maps.Clone(v.Cursor), Scroll: maps.Clone(v.Scroll), Split: maps.Clone(v.Split)}
}

// sameAsFresh asserts that what s drew incrementally equals what a new
// surface with the same state and view draws from scratch. This is the
// contract of the retained tree: dirty tracking may skip work, never
// change the picture.
func sameAsFresh(t testing.TB, s *Surface, w, h int) {
	t.Helper()
	got := s.Render(w, h)
	st, err := tml.NewStore(s.store.State(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f, err := New(s.doc, &s.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.SetPalette(s.pal)
	f.store = st
	f.view = cloneView(s.view)
	want := f.Render(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a, b := got.CellAt(x, y), want.CellAt(x, y)
			if a.Content != b.Content || a.Width != b.Width || !a.Style.Equal(&b.Style) {
				t.Fatalf("cell %d,%d: incremental %q %+v, fresh %q %+v\nincremental:\n%s\nfresh:\n%s", x, y,
					a.Content, a.Style, b.Content, b.Style, strings.Join(rows(got), "\n"), strings.Join(rows(want), "\n"))
			}
		}
	}
}

// TestPatchRedrawsOnlyReaders checks the retained tree's claim on the
// design's dashboard: a patch to one state path evaluates only the boxes
// that read it and repaints only their area, and the result equals a full
// redraw.
func TestPatchRedrawsOnlyReaders(t *testing.T) {
	s := fileSurface(t, "testdata/examples/dashboard.tml")
	const w, h = 64, 16
	s.Render(w, h)
	full := s.stats
	if err := s.Patch([]tml.Op{{Op: "replace", Path: "/skipped", Value: []byte("10")}}); err != nil {
		t.Fatal(err)
	}
	buf := s.Render(w, h)
	screenHas(t, buf, "10 skipped")
	t.Logf("full render: %d boxes, %d cells; patch: %d boxes, %d cells, damage %v", full.built, full.cells, s.stats.built, s.stats.cells, s.Damage())
	if s.stats.built != 1 {
		t.Errorf("a patch to /skipped evaluated %d boxes, want 1 (the stat that reads it)", s.stats.built)
	}
	if s.stats.cells*10 > full.cells {
		t.Errorf("a patch to /skipped wrote %d cells, more than a tenth of the %d of a full render", s.stats.cells, full.cells)
	}
	for _, d := range s.Damage() {
		if d.Dy() != 1 {
			t.Errorf("damage %v is more than the stats row", d)
		}
	}
	sameAsFresh(t, s, w, h)

	// A render with nothing changed paints nothing.
	s.Render(w, h)
	if s.stats.cells != 0 || len(s.Damage()) != 0 || s.stats.built != 0 {
		t.Errorf("an idle render did work: %+v, damage %v", s.stats, s.Damage())
	}

	// A patch that changes a row count rebuilds the rows' readers, moves
	// what is under them, and still equals a full redraw.
	if err := s.Patch([]tml.Op{{Op: "add", Path: "/failures/-", Value: []byte(`{"pkg": "tml", "test": "TestPatch", "ms": 5000}`)},
		{Op: "replace", Path: "/failed", Value: []byte("4")}}); err != nil {
		t.Fatal(err)
	}
	buf = s.Render(w, h)
	screenHas(t, buf, "TestPatch")
	screenHas(t, buf, "4 failed")
	sameAsFresh(t, s, w, h)
}

// TestLocalActions runs the dashboard's interactions with no owner: the
// cursor, sorting, a button's key, and enter on the table. The local ones
// change the screen at once. emit comes back as an Effect with its value.
func TestLocalActions(t *testing.T) {
	s := fileSurface(t, "testdata/examples/dashboard.tml")
	const w, h = 64, 16
	buf := s.Render(w, h)
	if y := rowWith(buf, "TestAttachRace"); y < 0 || !strings.Contains(rows(buf)[y], "▌") {
		t.Fatalf("the cursor starts on the slowest test:\n%s", strings.Join(rows(buf), "\n"))
	}
	res, err := s.Key("down")
	if err != nil || len(res.Effects) != 0 || !res.View {
		t.Fatalf("down: %+v %v", res, err)
	}
	buf = s.Render(w, h)
	if y := rowWith(buf, "TestDockClick"); !strings.Contains(rows(buf)[y], "▌") {
		t.Fatalf("down did not move the cursor:\n%s", strings.Join(rows(buf), "\n"))
	}
	sameAsFresh(t, s, w, h)

	// enter selects: on-select emits the row under the cursor.
	res, err = s.Key("enter")
	if err != nil || len(res.Effects) != 1 {
		t.Fatalf("enter: %+v %v", res, err)
	}
	e := res.Effects[0]
	row, _ := e.Value.(map[string]any)
	if e.Action != "emit" || e.Name != "open" || row["test"] != "TestDockClick" {
		t.Fatalf("enter emitted %+v", e)
	}

	// s sorts by the next column; the cursor keeps its row by key.
	if _, err := s.Key("s"); err != nil {
		t.Fatal(err)
	}
	buf = s.Render(w, h)
	if spec := s.view.Sort["/children/0/children/2/children/0"]; spec.Field != "pkg" {
		t.Fatalf("s sorted by %+v, want pkg", spec)
	}
	if y := rowWith(buf, "TestDockClick"); !strings.Contains(rows(buf)[y], "▌") {
		t.Fatalf("the cursor lost its row in the sort:\n%s", strings.Join(rows(buf), "\n"))
	}
	if rowWith(buf, "internal/app") > rowWith(buf, "vt ") {
		t.Fatalf("not sorted by package:\n%s", strings.Join(rows(buf), "\n"))
	}
	sameAsFresh(t, s, w, h)

	// A click on the TIME header sorts by time; a second click flips it.
	head := rowWith(buf, "PACKAGE")
	x := strings.Index(rows(buf)[head], "TIME")
	x = len([]rune(rows(buf)[head][:x]))
	for _, desc := range []bool{false, true} {
		if _, err := s.Mouse(Mouse{X: x, Y: head, Button: MouseLeft}); err != nil {
			t.Fatal(err)
		}
		s.Render(w, h)
		if spec := s.view.Sort["/children/0/children/2/children/0"]; spec.Field != "ms" || spec.Desc != desc {
			t.Fatalf("header click sorted by %+v, want ms desc=%v", spec, desc)
		}
	}
	sameAsFresh(t, s, w, h)

	// r presses the button with that key, anywhere on the surface.
	res, _ = s.Key("r")
	if len(res.Effects) != 1 || res.Effects[0].Name != "rerun" {
		t.Fatalf("r: %+v", res)
	}
	if rows, ok := res.Effects[0].Value.([]any); !ok || len(rows) != 3 {
		t.Fatalf("rerun carries %v, want the three failures", res.Effects[0].Value)
	}
}

// TestTabsAndFold switches a tab bound to state, by click and by key, and
// folds a card by clicking its title.
func TestTabsAndFold(t *testing.T) {
	s := mustSurface(t, `<tml version="1"><state>{"tab": "a", "n": 1}</state><col>
  <tabs value="state.tab" on-change="emit('tab', event.value)">
    <tab id="a" title="Alpha"><text>first page</text></tab>
    <tab id="b" title="Beta"><text>second page {state.n}</text></tab>
  </tabs>
  <card id="c" title="Details"><text>inside the card</text></card>
  <button key="x" on-press="set('n', state.n + 1); tab('b')">Next</button>
</col></tml>`)
	const w, h = 40, 10
	buf := s.Render(w, h)
	screenHas(t, buf, "first page")
	y := rowWith(buf, "Alpha")
	x := strings.Index(rows(buf)[y], "Beta")
	res, err := s.Mouse(Mouse{X: x, Y: y, Button: MouseLeft})
	if err != nil || s.store.Get("/tab") != "b" || len(res.Effects) != 1 || res.Effects[0].Value != "b" {
		t.Fatalf("click on Beta: %+v %v, state.tab=%v", res, err, s.store.Get("/tab"))
	}
	buf = s.Render(w, h)
	screenHas(t, buf, "second page 1")
	sameAsFresh(t, s, w, h)

	cy := rowWith(buf, "Details")
	if _, err := s.Mouse(Mouse{X: 3, Y: cy, Button: MouseLeft}); err != nil {
		t.Fatal(err)
	}
	buf = s.Render(w, h)
	if rowWith(buf, "inside the card") >= 0 || rowWith(buf, "▸ Details") < 0 {
		t.Fatalf("the card did not fold:\n%s", strings.Join(rows(buf), "\n"))
	}
	sameAsFresh(t, s, w, h)

	// set and tab run locally, in order, from one key.
	if _, err := s.Key("x"); err != nil {
		t.Fatal(err)
	}
	buf = s.Render(w, h)
	screenHas(t, buf, "second page 2")
	sameAsFresh(t, s, w, h)
}

// TestScrollAndSplit scrolls a table with the wheel and drags a split's
// divider.
func TestScrollAndSplit(t *testing.T) {
	var rowsJSON []string
	for i := range 40 {
		rowsJSON = append(rowsJSON, `{"n": `+strings.Repeat("1", 1)+`, "name": "row`+string(rune('A'+i%26))+string(rune('a'+i/26))+`"}`)
	}
	s := mustSurface(t, `<tml version="1"><state>{"rows": [`+strings.Join(rowsJSON, ",")+`]}</state>
<split dir="h" ratio="0.5"><table rows="state.rows" select="single"><column field="name"/></table><text>right side</text></split></tml>`)
	const w, h = 40, 8
	buf := s.Render(w, h)
	screenHas(t, buf, "rowAa")
	for range 2 {
		if _, err := s.Mouse(Mouse{X: 3, Y: 3, Button: WheelDown}); err != nil {
			t.Fatal(err)
		}
	}
	buf = s.Render(w, h)
	if rowWith(buf, "rowAa") >= 0 || rowWith(buf, "rowGa") < 0 {
		t.Fatalf("the wheel did not scroll the table:\n%s", strings.Join(rows(buf), "\n"))
	}
	sameAsFresh(t, s, w, h)

	before := strings.Index(rows(buf)[0], "right")
	div := 0
	for x := 0; x < w; x++ {
		if s.onDivider(s.root.kids[0], image.Point{X: x}) {
			div = x
		}
	}
	_, _ = s.Mouse(Mouse{X: div, Y: 0, Button: MouseLeft})
	_, _ = s.Mouse(Mouse{X: 30, Y: 0, Button: MouseLeft, Action: Motion})
	_, _ = s.Mouse(Mouse{X: 30, Y: 0, Button: MouseLeft, Action: Release})
	buf = s.Render(w, h)
	if after := strings.Index(rows(buf)[0], "right"); after <= before {
		t.Fatalf("the divider did not move: right side at %d, was %d", after, before)
	}
	sameAsFresh(t, s, w, h)
}

// TestFocusCycle moves focus with tab and gives keys to the focused node.
func TestFocusCycle(t *testing.T) {
	s := mustSurface(t, `<tml version="1"><col>
  <list items='["a", "b", "c"]' select="single" on-select="emit('pick', event.item)"><text>{item}</text></list>
  <button on-press="emit('ok')">OK</button>
</col></tml>`)
	s.Render(20, 6)
	if _, err := s.Key("down"); err != nil {
		t.Fatal(err)
	}
	res, _ := s.Key("enter")
	if len(res.Effects) != 1 || res.Effects[0].Value != "b" {
		t.Fatalf("enter on the list: %+v", res)
	}
	s.Render(20, 6)
	_, _ = s.Key("tab")
	s.Render(20, 6)
	res, _ = s.Key("enter")
	if len(res.Effects) != 1 || res.Effects[0].Name != "ok" {
		t.Fatalf("enter on the focused button: %+v", res)
	}
	sameAsFresh(t, s, 20, 6)
}

// TestControlCharsDropped draws a tree built in code, so tml.Sanitize never
// saw it: no C0 or C1 control character or DEL may reach a cell, since a
// cell's text goes to the terminal as is.
func TestControlCharsDropped(t *testing.T) {
	doc := &tml.Node{Type: "tml"}
	doc.SetAttr("version", tml.NumberValue(1))
	// A text run passes through tml's templates, which sanitize; code keeps
	// its text raw, so it reaches loom's own filter at the cell.
	txt := &tml.Node{Type: "text"}
	txt.Children = []*tml.Node{tml.TextNode("a\x1b[2Jb\u009b31mc\x7fd\x07e")}
	code := &tml.Node{Type: "code"}
	code.Children = []*tml.Node{tml.TextNode("a\x1b[2Jb\u009b31mc\x7fd\x07e")}
	doc.Children = []*tml.Node{txt, code}
	s, err := New(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	buf := s.Render(20, 2)
	checkFits(t, buf, 20, 2)
	for i, want := range []string{"a[2Jb31mcde", " a[2Jb31mcde"} {
		if got := strings.TrimRight(rows(buf)[i], " "); got != want {
			t.Fatalf("row %d is %q, want %q, the text with its control characters dropped", i, got, want)
		}
	}
}

// TestRelayoutWithoutRebuild changes the width of a table without changing
// its rows: the sibling text grows, the table is laid out again but not
// built again. Its column widths must follow the new width.
func TestRelayoutWithoutRebuild(t *testing.T) {
	s := mustSurface(t, `<tml version="1"><state>{"label": "x", "rows": [{"a": "alpha", "b": "beta"}, {"a": "gamma", "b": "delta"}]}</state>
<row gap="1"><text>{state.label}</text><table rows="state.rows" w="fill"><column field="a" w="fill"/><column field="b" w="fill"/></table></row></tml>`)
	s.Render(40, 4)
	if err := s.Set("state.label", "a much longer label"); err != nil {
		t.Fatal(err)
	}
	s.Render(40, 4)
	if s.stats.built != 1 {
		t.Fatalf("the patch built %d boxes, want only the text", s.stats.built)
	}
	sameAsFresh(t, s, 40, 4)
}
