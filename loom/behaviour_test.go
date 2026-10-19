package loom

import (
	"image"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
func rows(buf *Buffer) []string {
	var out []string
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			if c := buf.b.CellAt(x, y); c.Width > 0 {
				b.WriteString(c.Content)
			}
		}
		out = append(out, b.String())
	}
	return out
}

func screenHas(t *testing.T, buf *Buffer, want string) {
	t.Helper()
	for _, r := range rows(buf) {
		if strings.Contains(r, want) {
			return
		}
	}
	t.Fatalf("the screen has no %q:\n%s", want, strings.Join(rows(buf), "\n"))
}

// rowWith returns the row that holds s, or -1.
func rowWith(buf *Buffer, s string) int {
	for i, r := range rows(buf) {
		if strings.Contains(r, s) {
			return i
		}
	}
	return -1
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
			a, b := got.b.CellAt(x, y), want.b.CellAt(x, y)
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
	s.Damage() // the first render's
	if err := s.Patch([]tml.Op{{Op: "replace", Path: "/skipped", Value: []byte("10")}}); err != nil {
		t.Fatal(err)
	}
	buf := s.Render(w, h)
	screenHas(t, buf, "10 skipped")
	damage := s.Damage()
	t.Logf("full render: %d boxes, %d cells; patch: %d boxes, %d cells, damage %v", full.built, full.cells, s.stats.built, s.stats.cells, damage)
	if len(damage) == 0 {
		t.Error("the patch reported no damage")
	}
	if s.stats.built != 1 {
		t.Errorf("a patch to /skipped evaluated %d boxes, want 1 (the stat that reads it)", s.stats.built)
	}
	if s.stats.cells*10 > full.cells {
		t.Errorf("a patch to /skipped wrote %d cells, more than a tenth of the %d of a full render", s.stats.cells, full.cells)
	}
	for _, d := range damage {
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

// TestHugeBoundNumbers binds every number that sizes something to values a
// hostile document can send: 1e10, -1e10, 1e308 and NaN. tml.Check does not
// range-check a binding, so loom must. Before the clamp, bins of 1e10 asked
// plot for 80 GB, which the Go runtime ends with a fatal error. The test
// runs under the limits of the machine's test wrapper (ulimit -v and
// GOMEMLIMIT), and also counts what the render allocates.
func TestHugeBoundNumbers(t *testing.T) {
	for _, v := range []string{"1e10", "-1e10", "1e308", "'NaN'"} {
		src := `<tml version="1"><state>{"v": [1, 2, 3, 4, 5, 6, 7, 8]}</state><col gap="{` + v + `}">
  <histogram values="state.v" bins="{` + v + `}"/>
  <textarea name="t" label="t" rows="{` + v + `}"/>
  <heading level="{` + v + `}">h</heading>
  <tree items='[{"name": "a", "children": [{"name": "b"}]}]' open="{` + v + `}"/>
  <row gap="{` + v + `}"><text>a</text></row>
  <line data="state.v" y="` + strings.Repeat("a,", 5000) + `a"/>
  <slider name="s" label="s" value="{` + v + `}" min="{` + v + `}" max="{` + v + `}"/>
</col></tml>`
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		s := mustSurface(t, src)
		buf := s.Render(80, 40)
		runtime.ReadMemStats(&after)
		checkFits(t, buf, 80, 40)
		if used := after.TotalAlloc - before.TotalAlloc; used > 64<<20 {
			t.Errorf("value %s: one render allocated %d MB", v, used>>20)
		}
	}
}

// TestHiddenTakesNoInput hides a button with show: its key, tab and a click
// must not reach it. Before the fix the hidden box's children were still
// laid out and joined the focus list, so d fired delete with nothing on
// the screen.
func TestHiddenTakesNoInput(t *testing.T) {
	s := mustSurface(t, `<tml version="1"><state>{"confirm": false}</state><col>
  <col show="state.confirm"><button key="d" on-press="emit('delete')">Delete</button></col>
  <button key="k" on-press="emit('keep')">Keep</button>
</col></tml>`)
	s.Render(30, 4)
	if res, _ := s.Key("d"); len(res.Effects) != 0 {
		t.Fatalf("d on a hidden button emitted %+v", res.Effects)
	}
	if res, _ := s.Mouse(Mouse{X: 2, Y: 0, Button: MouseLeft}); len(res.Effects) != 0 {
		t.Fatalf("a click on a hidden button emitted %+v", res.Effects)
	}
	for _, b := range s.focus {
		if bw, ok := b.w.(*buttonWidget); ok && bw.key == "d" {
			t.Fatal("the hidden button is in the focus order")
		}
	}
	if err := s.Set("state.confirm", true); err != nil {
		t.Fatal(err)
	}
	s.Render(30, 4)
	if res, _ := s.Key("d"); len(res.Effects) != 1 || res.Effects[0].Name != "delete" {
		t.Fatalf("d on the shown button: %+v", res.Effects)
	}
}

// TestBudgetPerBox renders a list whose bindings together pass one frame's
// 10,000 steps. With one budget for the frame the last items showed "!"
// in a full render and their values after an incremental one. Each box
// now has its own budget, so every item shows its value either way.
func TestBudgetPerBox(t *testing.T) {
	var items []string
	for i := range 1500 {
		items = append(items, `{"a": "item`+strconv.Itoa(i)+`"}`)
	}
	s := mustSurface(t, `<tml version="1"><state>{"rows": [`+strings.Join(items, ",")+`]}</state>
<list items="state.rows"><text>{item.a} {upper(item.a)} {lower(item.a)}</text></list></tml>`)
	s.Render(40, 10)
	bad := 0
	var walk func(b *box)
	walk = func(b *box) {
		if tw, ok := b.w.(*textWidget); ok {
			for _, sp := range tw.spans {
				if strings.Contains(sp.text, "!") {
					bad++
				}
			}
		}
		for _, k := range b.kids {
			walk(k)
		}
	}
	walk(s.root)
	if bad > 0 {
		t.Fatalf("%d items ran out of budget", bad)
	}
}

// TestClockTick: a binding that calls ago() changes only after Tick, and
// Tick evaluates only the boxes that call it.
func TestClockTick(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	doc, _, err := tml.Check([]byte(`<tml version="1"><state>{"t": 999940}</state><col><text>{ago(state.t)}</text><text>static</text></col></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := New(doc, &Options{Now: func() time.Time { return now }})
	if !s.NeedsClock() {
		t.Fatal("a surface with ago() needs no clock")
	}
	first := strings.TrimSpace(rows(s.Render(20, 2))[0])
	now = now.Add(10 * time.Minute)
	s.Render(20, 2)
	if got := strings.TrimSpace(rows(s.Render(20, 2))[0]); got != first {
		t.Fatalf("the text changed without a tick: %q", got)
	}
	s.Tick()
	second := strings.TrimSpace(rows(s.Render(20, 2))[0])
	if second == first || s.stats.built != 1 {
		t.Fatalf("after a tick: %q (was %q), %d boxes built", second, first, s.stats.built)
	}
	plain := mustSurface(t, `<tml version="1"><text>static</text></tml>`)
	if plain.NeedsClock() {
		t.Fatal("a surface without ago() asks for a clock")
	}
}

// TestLinkNeedsConfirm: a click on a link comes back as a link effect for
// the host to confirm, and no OSC 8 link reaches the cells unless the host
// asks for it.
func TestLinkNeedsConfirm(t *testing.T) {
	src := `<tml version="1"><text>see <a href="https://tuios.dev">the site</a></text></tml>`
	s := mustSurface(t, src)
	buf := s.Render(30, 1)
	for x := 0; x < 30; x++ {
		if c := buf.b.CellAt(x, 0); c.Link.URL != "" {
			t.Fatalf("cell %d carries an OSC 8 link", x)
		}
	}
	res, _ := s.Mouse(Mouse{X: 5, Y: 0, Button: MouseLeft})
	if len(res.Effects) != 1 || res.Effects[0].Action != "link" || res.Effects[0].Name != "https://tuios.dev" {
		t.Fatalf("a click on the link gave %+v", res.Effects)
	}
	doc, _, _ := tml.Check([]byte(src), nil)
	s2, _ := New(doc, &Options{Hyperlinks: true})
	if c := s2.Render(30, 1).b.CellAt(5, 0); c.Link.URL != "https://tuios.dev" {
		t.Fatalf("with Hyperlinks the cell has link %q", c.Link.URL)
	}
}

// TestIfInListTemplates patches an if that two list templates read: list A
// has 1000 items and list B 500. The rebuild built list A once per item, as
// orphans that spent the shared expansion budget, so list B drew empty.
// The incremental picture must equal a full one, with no orphan boxes.
func TestIfInListTemplates(t *testing.T) {
	var a, b []string
	for i := range 1000 {
		a = append(a, `{"x": "a`+strconv.Itoa(i)+`"}`)
	}
	for i := range 500 {
		b = append(b, `{"x": "b`+strconv.Itoa(i)+`"}`)
	}
	s := mustSurface(t, `<tml version="1"><state>{"more": true, "a": [`+strings.Join(a, ",")+`], "b": [`+strings.Join(b, ",")+`]}</state>
<row><list items="state.a"><text if="state.more">{item.x}</text></list><list items="state.b"><text if="state.more">{item.x}</text></list></row></tml>`)
	s.Render(40, 10)
	for _, v := range []string{"false", "true"} {
		if err := s.Patch([]tml.Op{{Op: "replace", Path: "/more", Value: []byte(v)}}); err != nil {
			t.Fatal(err)
		}
		sameAsFresh(t, s, 40, 10)
		assertReachable(t, s)
	}
	if rowWith(s.Render(40, 10), "b0") < 0 {
		t.Fatal("list B drew empty after the patch")
	}
}

// TestFrameBudget: the bindings of one frame spend at most frameSteps in
// all, however many boxes there are, because each box gets its share.
// With few boxes the share is large, so a heavy binding still evaluates.
func TestFrameBudget(t *testing.T) {
	big := `"` + strings.Repeat("x", 20000) + `"`
	var items []string
	for range 1500 {
		items = append(items, `0`)
	}
	s := mustSurface(t, `<tml version="1"><state>{"big": `+big+`, "rows": [`+strings.Join(items, ",")+`]}</state>
<list items="state.rows"><text>{upper(state.big)}{lower(state.big)}</text></list></tml>`)
	s.Render(40, 10)
	t.Logf("1500 heavy boxes: share %d, %d steps", s.boxSteps, s.stats.steps)
	if s.stats.steps > frameSteps {
		t.Fatalf("one frame spent %d steps, the budget is %d", s.stats.steps, frameSteps)
	}
	one := mustSurface(t, `<tml version="1"><state>{"big": `+big+`}</state><text>{len(upper(state.big))}</text></tml>`)
	if got := strings.TrimSpace(rows(one.Render(20, 1))[0]); got != "20000" {
		t.Fatalf("a lone heavy binding gave %q, want 20000", got)
	}
}

// TestFocusBelowTheFold: tab reaches a button that a scroll hides and
// brings it into view; after a wheel scroll hides it again, enter still
// presses it. Before, the hidden button left the focus order, so focus
// fell back to the first table and enter ran its on-select.
func TestFocusBelowTheFold(t *testing.T) {
	var buttons []string
	for i := range 6 {
		buttons = append(buttons, `<button on-press="emit('b`+strconv.Itoa(i)+`')">button `+strconv.Itoa(i)+`</button>`)
	}
	s := mustSurface(t, `<tml version="1"><col>
  <table rows='[{"a": "one"}, {"a": "two"}]' select="single" on-select="emit('sel')"><column field="a"/></table>
  <scroll h="3">`+strings.Join(buttons, "")+`</scroll>
</col></tml>`)
	const w, h = 30, 6
	buf := s.Render(w, h)
	if rowWith(buf, "button 5") >= 0 {
		t.Fatal("button 5 is on screen at the start")
	}
	for range 6 { // from the table, which has the focus first, to button 5
		if _, err := s.Key("tab"); err != nil {
			t.Fatal(err)
		}
		buf = s.Render(w, h)
	}
	if s.view.Focus == "" || rowWith(buf, "button 5") < 0 {
		t.Fatalf("tab did not bring button 5 into view (focus %q):\n%s", s.view.Focus, strings.Join(rows(buf), "\n"))
	}
	sameAsFresh(t, s, w, h)
	for range 3 {
		_, _ = s.Mouse(Mouse{X: 2, Y: 4, Button: WheelUp})
	}
	buf = s.Render(w, h)
	if rowWith(buf, "button 5") >= 0 {
		t.Fatal("the wheel did not scroll button 5 away")
	}
	res, _ := s.Key("enter")
	if len(res.Effects) != 1 || res.Effects[0].Name != "b5" {
		t.Fatalf("enter with button 5 focused but scrolled away gave %+v", res.Effects)
	}
}

// TestDamageAcrossInput: a patch, then a key, then the host's Render. The
// key's own render applied the patch first; its damage must still reach
// the host, or a host that copies only damaged cells shows stale text.
func TestDamageAcrossInput(t *testing.T) {
	s := fileSurface(t, "testdata/examples/dashboard.tml")
	const w, h = 64, 16
	s.Render(w, h)
	s.Damage()
	if err := s.Patch([]tml.Op{{Op: "replace", Path: "/skipped", Value: []byte("10")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Key("down"); err != nil {
		t.Fatal(err)
	}
	s.Render(w, h)
	stats := rowWith(s.out, "10 skipped")
	hit := false
	for _, d := range s.Damage() {
		if stats >= d.Min.Y && stats < d.Max.Y {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("the stats row %d was repainted but is not in the damage", stats)
	}
	a := s.Damage()
	if len(a) != 0 {
		t.Fatalf("damage read twice: %v", a)
	}
}
