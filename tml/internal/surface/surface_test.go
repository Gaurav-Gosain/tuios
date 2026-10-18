package surface

import (
	"os"
	"reflect"
	"testing"
	"time"

	"tuios.dev/tml"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDepsAffected checks that a patch reaches the nodes that read the
// changed paths, and no others.
func TestDepsAffected(t *testing.T) {
	doc, err := tml.Parse(readFile(t, "../../testdata/examples/dashboard.tml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	d := BuildDeps(doc)
	got := d.Affected([]string{"/failed"})
	want := []string{"/children/0/children/0/children/1", "/children/0/children/0/children/4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("/failed affects %v, want %v", got, want)
	}
	got = d.Affected([]string{"/failures/0/ms"})
	want = []string{"/children/0/children/2", "/children/0/children/2/children/0", "/children/0/children/2/children/0/children/2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("/failures/0/ms affects %v, want %v", got, want)
	}
	if got := d.Affected([]string{"/nothing"}); len(got) != 0 {
		t.Errorf("/nothing affects %v", got)
	}
	if len(d.Clock()) != 0 {
		t.Errorf("the dashboard needs no clock, got %v", d.Clock())
	}
	clock, _ := tml.Parse([]byte(`<tml version="1"><state>{"items":[{"at":1}]}</state><list items="state.items"><text>{ago(item.at)}</text></list></tml>`), nil)
	cd := BuildDeps(clock)
	if !reflect.DeepEqual(cd.Clock(), []string{"/children/0/children/0"}) {
		t.Errorf("clock nodes %v", cd.Clock())
	}
	if !reflect.DeepEqual(cd.Of("/children/0/children/0"), []string{"/items"}) {
		t.Errorf("item.at resolves to %v, want /items", cd.Of("/children/0/children/0"))
	}
}

// TestActions runs the local actions and collects the owner's effects.
func TestActions(t *testing.T) {
	doc, err := tml.Parse([]byte(`<tml version="1">
  <state>{"open": false, "n": 1}</state>
  <col>
    <tabs id="t"><tab id="a" title="A"><text>a</text></tab><tab id="b" title="B"><text>b</text></tab></tabs>
    <tabs value="state.tab"><tab id="c" title="C"><text>c</text></tab></tabs>
    <table id="tbl" rows="state.rows" sort="ms:desc"><column field="ms"/></table>
    <card id="k" fold="true"><text>x</text></card>
  </col>
</tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := tml.StoreFor(doc, nil)
	view := NewView()
	run := func(src string, env *tml.Env) Result {
		t.Helper()
		acts, err := tml.ParseActions(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		res, err := RunActions(doc, store, view, "/children/0/children/2", acts, env)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return res
	}
	res := run("set('state.n', state.n + 1); emit('saved', state.n); toggle('open')", nil)
	if store.Get("/n") != 2.0 || store.Get("/open") != true {
		t.Errorf("state %v", store.State())
	}
	if len(res.Effects) != 1 || res.Effects[0].Name != "saved" || res.Effects[0].Value != 2.0 {
		t.Errorf("effects %+v", res.Effects)
	}
	run("tab('b')", nil)
	if view.Tabs["t"] != "b" {
		t.Errorf("view tabs %v", view.Tabs)
	}
	res = run("tab('c')", nil)
	if store.Get("/tab") != "c" || len(res.Changed) == 0 {
		t.Errorf("a lifted tab did not write state: %v", store.State())
	}
	run("sort('ms')", nil)
	if view.Sort["tbl"] != (SortSpec{"ms", false}) {
		t.Errorf("sort %v: the first sort of the current field flips it", view.Sort)
	}
	run("sort('tbl', 'name')", nil)
	if view.Sort["tbl"] != (SortSpec{"name", false}) {
		t.Errorf("sort %v", view.Sort)
	}
	run("filter('tbl', event.value); fold('k'); focus('tbl')", &tml.Env{Vars: map[string]any{"event": map[string]any{"value": "race"}}})
	if view.Filter["tbl"] != "race" || view.Fold["k"] != false || view.Focus != "tbl" {
		t.Errorf("view %+v", view)
	}
	res = run("close(null); copy('x'); open('file:a.md'); pane('focus', {target: 'api'})", nil)
	if len(res.Effects) != 4 || !res.Effects[0].HasValue || res.Effects[2].Name != "a.md" || res.Effects[3].Name != "focus" {
		t.Errorf("effects %+v", res.Effects)
	}
	rows := []any{map[string]any{"ms": 5.0, "t": "b"}, map[string]any{"ms": 50.0, "t": "A"}, map[string]any{"t": "c"}}
	sorted := SortRows(rows, SortSpec{"ms", true})
	if sorted[0].(map[string]any)["ms"] != 50.0 || sorted[2].(map[string]any)["t"] != "c" {
		t.Errorf("sorted %v", sorted)
	}
	if got := FilterRows(rows, "A"); len(got) != 1 {
		t.Errorf("filtered %v", got)
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(PatchesPerSecond, PatchesPerSecond)
	now := time.Unix(0, 0)
	n := 0
	for range 150 {
		if l.Allow(now) {
			n++
		}
	}
	if n != PatchesPerSecond {
		t.Errorf("allowed %d patches at once, want %d", n, PatchesPerSecond)
	}
	if !l.Allow(now.Add(20 * time.Millisecond)) {
		t.Error("no patch allowed after the bucket refilled")
	}
	toast := NewLimiter(1/ToastInterval.Seconds(), 1)
	if !toast.Allow(now) || toast.Allow(now.Add(time.Second)) || !toast.Allow(now.Add(2*time.Second)) {
		t.Error("the toast limit is not one per two seconds")
	}
}

// A computed index ends the dependency path, so a patch to any element
// reaches the node. Before the fix the path read /items/name and the screen
// went stale.
func TestDepsDynamicIndex(t *testing.T) {
	doc, err := tml.Parse([]byte(`<tml version="1"><state>{"i":0,"items":[{"name":"a"}]}</state><text>{state.items[state.i].name}</text></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	d := BuildDeps(doc)
	if got := d.Of("/children/0"); !reflect.DeepEqual(got, []string{"/i", "/items"}) {
		t.Errorf("deps %v", got)
	}
	if got := d.Affected([]string{"/items/0/name"}); !reflect.DeepEqual(got, []string{"/children/0"}) {
		t.Errorf("affected %v", got)
	}
}

func TestExpansion(t *testing.T) {
	e := NewExpansion(nil)
	if !e.Take(1999) || e.Take(2) || !e.Take(1) || e.Left() != 0 {
		t.Error("the expansion budget does not hold 2,000 nodes")
	}
}
