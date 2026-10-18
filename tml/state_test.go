package tml

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestPatchRFC6902 runs examples from RFC 6902 appendix A.
func TestPatchRFC6902(t *testing.T) {
	cases := []struct{ doc, patch, want string }{
		{`{"foo":"bar"}`, `[{"op":"add","path":"/baz","value":"qux"}]`, `{"baz":"qux","foo":"bar"}`},
		{`{"foo":["bar","baz"]}`, `[{"op":"add","path":"/foo/1","value":"qux"}]`, `{"foo":["bar","qux","baz"]}`},
		{`{"baz":"qux","foo":"bar"}`, `[{"op":"remove","path":"/baz"}]`, `{"foo":"bar"}`},
		{`{"foo":["bar","qux","baz"]}`, `[{"op":"remove","path":"/foo/1"}]`, `{"foo":["bar","baz"]}`},
		{`{"baz":"qux","foo":"bar"}`, `[{"op":"replace","path":"/baz","value":"boo"}]`, `{"baz":"boo","foo":"bar"}`},
		{`{"foo":{"bar":"baz","waldo":"fred"},"qux":{"corge":"grault"}}`, `[{"op":"move","from":"/foo/waldo","path":"/qux/thud"}]`,
			`{"foo":{"bar":"baz"},"qux":{"corge":"grault","thud":"fred"}}`},
		{`{"foo":["all","grass","cows","eat"]}`, `[{"op":"move","from":"/foo/1","path":"/foo/3"}]`, `{"foo":["all","cows","eat","grass"]}`},
		{`{"baz":"qux","foo":["a",2,"c"]}`, `[{"op":"test","path":"/baz","value":"qux"},{"op":"test","path":"/foo/1","value":2}]`, `{"baz":"qux","foo":["a",2,"c"]}`},
		{`{"foo":"bar"}`, `[{"op":"add","path":"/child","value":{"grandchild":{}}}]`, `{"foo":"bar","child":{"grandchild":{}}}`},
		{`{"foo":["bar"]}`, `[{"op":"add","path":"/foo/-","value":["abc","def"]}]`, `{"foo":["bar",["abc","def"]]}`},
		{`{"/":9,"~1":10}`, `[{"op":"test","path":"/~01","value":10}]`, `{"/":9,"~1":10}`},
		{`{"a":{"b":1}}`, `[{"op":"copy","from":"/a","path":"/c"}]`, `{"a":{"b":1},"c":{"b":1}}`},
	}
	for _, c := range cases {
		ops, err := DecodePatch([]byte(c.patch), DefaultLimits())
		if err != nil {
			t.Fatalf("%s: %v", c.patch, err)
		}
		got, err := ApplyPatch(decode(t, c.doc), ops)
		if err != nil {
			t.Errorf("%s: %v", c.patch, err)
			continue
		}
		if !reflect.DeepEqual(got, decode(t, c.want)) {
			t.Errorf("%s: got %v", c.patch, got)
		}
	}
	for _, c := range []struct{ doc, patch string }{
		{`{"foo":"bar"}`, `[{"op":"add","path":"/baz/bat","value":"qux"}]`},
		{`{"baz":"qux"}`, `[{"op":"test","path":"/baz","value":"bar"}]`},
		{`{"foo":[1]}`, `[{"op":"add","path":"/foo/5","value":2}]`},
		{`{"foo":[1]}`, `[{"op":"remove","path":"/foo/01"}]`},
		{`{"a":{"b":1}}`, `[{"op":"move","from":"/a","path":"/a/b/c"}]`},
		{`{}`, `[{"op":"replace","path":"/x","value":1}]`},
	} {
		ops, err := DecodePatch([]byte(c.patch), DefaultLimits())
		if err != nil {
			continue
		}
		doc := decode(t, c.doc)
		if _, err := ApplyPatch(doc, ops); err == nil {
			t.Errorf("%s applied; it should not", c.patch)
		}
		if !reflect.DeepEqual(doc, decode(t, c.doc)) {
			t.Errorf("%s changed the document on failure", c.patch)
		}
	}
}

// TestStore checks atomic patches, the size limit and the changed paths.
func TestStore(t *testing.T) {
	lim := DefaultLimits()
	lim.StateBytes = 64
	s, err := NewStore(map[string]any{"n": 1.0, "items": []any{}}, lim)
	if err != nil {
		t.Fatal(err)
	}
	ops, _ := DecodePatch([]byte(`[{"op":"replace","path":"/n","value":2},{"op":"test","path":"/n","value":3}]`), lim)
	if _, err := s.Apply(ops); err == nil {
		t.Fatal("a failed test applied")
	}
	if s.Get("/n") != 1.0 || s.Version() != 0 {
		t.Fatalf("a failed patch changed the store: %v", s.State())
	}
	ops, _ = DecodePatch([]byte(`[{"op":"add","path":"/items/-","value":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`), lim)
	if _, err := s.Apply(ops); err == nil {
		t.Fatal("a patch past the state limit applied")
	}
	changed, err := s.Set("state.form.title", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changed, []string{"/form", "/form/title"}) || s.Get("/form/title") != "x" {
		t.Errorf("changed %v, state %v", changed, s.State())
	}
	if _, err := DecodePatch(make([]byte, lim.PatchBytes+1), lim); err == nil {
		t.Error("a patch past the size limit decoded")
	}
}

// TestDepsAffected checks that a patch reaches the nodes that read the
// changed paths, and no others.
func TestDepsAffected(t *testing.T) {
	doc, err := Parse(readFile(t, "testdata/examples/dashboard.tml"), nil)
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
	clock, _ := Parse([]byte(`<tml version="1"><state>{"items":[{"at":1}]}</state><list items="state.items"><text>{ago(item.at)}</text></list></tml>`), nil)
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
	doc, err := Parse([]byte(`<tml version="1">
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
	store, _ := StoreFor(doc, DefaultLimits())
	view := NewView()
	run := func(src string, env *Env) Result {
		t.Helper()
		acts, err := ParseActions(src)
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
	run("filter('tbl', event.value); fold('k'); focus('tbl')", &Env{Vars: map[string]any{"event": map[string]any{"value": "race"}}})
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
