package tml

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// These tests hold the findings of the wave 1 security review. Each one
// is a small input that beat a limit before the fix. The review's probe
// program reproduced each with the same input.

// allocated returns the bytes f allocates.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// High 1: copies of the root grew the state exponentially before the size
// check ran. 18 copies of 675 bytes allocated 122 MB.
func TestReviewPatchGrowth(t *testing.T) {
	// 12 copies of a root of 500 members would make 2 million map entries,
	// about 200 MB, without the check after each operation. The sizes are
	// kept small so that a build without the fix fails this test instead of
	// exhausting the machine.
	var ops []string
	for i := range 12 {
		ops = append(ops, fmt.Sprintf(`{"op":"copy","from":"","path":"/a%d"}`, i))
	}
	patch, err := DecodePatch([]byte("["+strings.Join(ops, ",")+"]"), nil)
	if err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits()
	lim.StateBytes = 64 << 10
	root := map[string]any{}
	for i := range 500 {
		root[fmt.Sprintf("k%d", i)] = 1.0
	}
	s, err := NewStore(root, &lim)
	if err != nil {
		t.Fatal(err)
	}
	var applyErr error
	mem := allocated(func() { _, applyErr = s.Apply(patch) })
	var pe *PatchError
	if !errors.As(applyErr, &pe) || pe.Code != CodeLimit {
		t.Fatalf("a patch of root copies applied or failed wrongly: %v", applyErr)
	}
	if mem > 32<<20 {
		t.Errorf("the refused patch allocated %d MB before it stopped", mem>>20)
	}
	if len(s.State()) != 500 {
		t.Errorf("a refused patch changed the state")
	}
}

// High 2: JSON escapes reached the state, patch values and Set, and Eval
// returned arrays and objects unsanitized.
func TestReviewEscapesInState(t *testing.T) {
	doc, _, err := Check([]byte(`<tml version="1"><state>{"s":"\u001b]52;c;eA==\u0007X\u202eY","rows":[{"n\u001b":"\u001b[2J"}]}</state><text>{state.rows}</text></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := StoreFor(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := st.State()["rows"].([]any)[0].(map[string]any)
	if row["n"] != "[2J" || st.State()["s"] != "]52;c;eA==XY" {
		t.Errorf("the store kept an escape: %q", st.State())
	}
	ops, _ := DecodePatch([]byte(`[{"op":"add","path":"/p","value":["\u001b[31mred"]}]`), nil)
	if _, err := st.Apply(ops); err != nil {
		t.Fatal(err)
	}
	if p := st.State()["p"].([]any)[0]; p != "[31mred" {
		t.Errorf("a patch value kept an escape: %q", p)
	}
	if _, err := st.Set("state.q", map[string]any{"k\x1b": "\u009bv"}); err != nil {
		t.Fatal(err)
	}
	host := map[string]any{"event": map[string]any{"v": []any{"\x1b[2J"}}}
	tmpl, _ := ParseTemplate("{[state.p, state.q, event.v]}")
	v, err := tmpl.Eval(&Env{Vars: map[string]any{"state": st.State(), "event": host["event"]}})
	if err != nil {
		t.Fatal(err)
	}
	if s := Stringify(v); strings.ContainsAny(s, "\x1b\u009b") || s != `[["[31mred"],{"k":"v"},["[2J"]]` {
		t.Errorf("Eval returned an escape: %q", s)
	}
}

// High 3: once the problem slots were full of warnings, errors were dropped
// and Check returned nil. Problems 0 turned validation off.
func TestReviewProblemCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<tml version="1"><state>{}</state>`)
	for range 20 {
		b.WriteString(`<text>{state.nope}</text>`)
	}
	for range 50 {
		b.WriteString(`<image src="file:a.png" alt="x"/>`)
	}
	b.WriteString(`<blink/><row on-press="pane('send-text')"/></tml>`)
	_, problems, err := Check([]byte(b.String()), nil)
	if err == nil {
		t.Fatal("errors after 20 warnings were dropped")
	}
	if problems[len(problems)-1].Severity != SevError {
		t.Errorf("the last slot holds a warning: %v", problems[len(problems)-1])
	}
	lim := DefaultLimits()
	lim.Problems = 0
	if _, _, err := Check([]byte(`<tml version="1"><blink/></tml>`), &ValidateOptions{Limits: &lim}); err == nil {
		t.Error("Problems: 0 turned validation off")
	}
}

// High 4: nested each, literal arrays and paths through an element name
// were not counted.
func TestReviewEachExpansion(t *testing.T) {
	forty := "[" + strings.TrimSuffix(strings.Repeat("0,", 40), ",") + "]"
	cases := map[string]string{
		"nested":  `<tml version="1"><state>{"a":` + forty + `}</state><col each="state.a"><col each="state.a"><col each="state.a"><col each="state.a"><text>x</text></col></col></col></col></tml>`,
		"literal": `<tml version="1"><col each="[` + strings.Repeat("0,", 60000) + `0]"><text>a</text><text>b</text></col></tml>`,
		"alias":   `<tml version="1"><state>{"rows":[{"kids":` + forty + `},{"kids":` + forty + `}]}</state><list items="state.rows"><col each="item.kids"><text each="` + forty + `">x</text></col></list></tml>`,
		"images":  `<tml version="1"><state>{"a":[1,2,3]}</state><col each="state.a"><image src="file:a.png" alt="a"/><image src="file:b.png" alt="b"/></col></tml>`,
	}
	for name, src := range cases {
		_, problems, err := Check([]byte(src), nil)
		if err == nil || problems[len(problems)-1].Code != CodeLimit {
			t.Errorf("%s: want a limit problem, got %v", name, problems)
		}
	}
}

// High 5: 364 fmt(state) calls against a 1 MB state took 1.6 s and 778 MB
// for 729 steps.
func TestReviewStepCharging(t *testing.T) {
	rows := make([]any, 20000)
	for i := range rows {
		rows[i] = map[string]any{"name": strings.Repeat("x", 40)}
	}
	s, err := NewStore(map[string]any{"rows": rows}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		"[" + strings.TrimSuffix(strings.Repeat("fmt(state),", 364), ",") + "]",
		"[" + strings.TrimSuffix(strings.Repeat("len(fmt(state.rows)),", 190), ",") + "]",
		"[" + strings.TrimSuffix(strings.Repeat("state.rows == state.rows,", 160), ",") + "]",
		"[" + strings.TrimSuffix(strings.Repeat("upper(join(pick(state.rows, 'name'))),", 100), ",") + "]",
	} {
		e, err := ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		var evalErr error
		mem := allocated(func() { _, evalErr = e.Eval(&Env{Vars: map[string]any{"state": s.State()}}) })
		if !errors.Is(evalErr, ErrBudget) {
			t.Errorf("%.40s...: got %v, want ErrBudget", src, evalErr)
		}
		if mem > 64<<20 {
			t.Errorf("%.40s...: allocated %d MB", src, mem>>20)
		}
	}
	if got := len(Stringify(s.State())); got > maxString {
		t.Errorf("Stringify returned %d bytes", got)
	}
}

// High 6: adjacent JSON strings and comment-split text took quadratic time.
func TestReviewLinearParsing(t *testing.T) {
	lim := PersonLimits()
	var b strings.Builder
	b.WriteString(`{"type":"tml","version":1,"children":[{"type":"text","children":[`)
	for i := range 64000 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"a"`)
	}
	b.WriteString(`]}]}`)
	jsonSrc := []byte(b.String())
	xmlSrc := []byte(`<tml version="1"><text>` + strings.Repeat("a<!---->", 120000) + `</text></tml>`)
	for name, parse := range map[string]func() error{
		"json": func() error { _, err := ParseJSON(jsonSrc, &ParseOptions{Limits: &lim}); return err },
		"xml":  func() error { _, err := ParseXML(xmlSrc, &ParseOptions{Limits: &lim}); return err },
	} {
		var err error
		mem := allocated(func() { err = parse() })
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if mem > 64<<20 {
			t.Errorf("%s: allocated %d MB", name, mem>>20)
		}
	}
	// Many unknown components or attributes: the suggestion work stops when
	// the problem slots are full.
	b.Reset()
	b.WriteString(`<tml version="1"><row>`)
	for b.Len() < 250000 {
		b.WriteString(`<qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq/>`)
	}
	b.WriteString(`</row></tml>`)
	if mem := allocated(func() { _, _, _ = Check([]byte(b.String()), nil) }); mem > 64<<20 {
		t.Errorf("unknown components: allocated %d MB", mem>>20)
	}
}

// Medium 7: function results and unary minus could be Inf or NaN.
func TestReviewFinite(t *testing.T) {
	for _, src := range []string{"sum([1e308,1e308])", "-sum([1e308,1e308])", "pct(1e308, 1e-308)",
		"round(1e308, 10)", "dur(1e308, 'h')", "abs(-sum([1e308,1e308]))"} {
		e, err := ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := e.Eval(nil)
		tmpl, _ := ParseTemplate("x{" + src + "}")
		if v != nil || tmpl.Render(nil) != "x" {
			t.Errorf("%s = %v, renders %q; want null", src, v, tmpl.Render(nil))
		}
	}
}

// Medium 9: Check accepted a state the store refuses.
func TestReviewStateLoads(t *testing.T) {
	_, problems, err := Check([]byte(`{"type":"tml","version":1,"state":{"x":1e999}}`), nil)
	if err == nil || len(problems) == 0 {
		t.Errorf("a state the store refuses passed Check: %v", problems)
	}
	lim := DefaultLimits()
	lim.StateBytes = 16
	_, problems, err = Check([]byte(`<tml version="1"><state>{"a":"0123456789abcdef"}</state></tml>`), &ValidateOptions{Limits: &lim})
	if err == nil || problems[0].Code != CodeLimit {
		t.Errorf("a state over the limit passed Check: %v", problems)
	}
}

// Medium 10: two cases did not round trip.
func TestReviewRoundTrip(t *testing.T) {
	j := []byte(`{"type":"tml","state":{"a":1},"version":1,"children":[{"type":"text","children":["hi"]}]}`)
	d, err := ParseJSON(j, nil)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseXML(FormatXML(d), nil)
	if err != nil || string(FormatJSON(back, false)) != string(FormatJSON(d, false)) {
		t.Errorf("state before version does not round trip: %s", FormatJSON(back, false))
	}
	if _, err := ParseXML([]byte(`<tml version="1"><code children="x"> </code></tml>`), nil); err == nil {
		t.Error("a children attribute beside raw text parsed")
	}
}

// Medium 11: did_you_mean named components that are not legal there.
func TestReviewLegalSuggestions(t *testing.T) {
	for src, bad := range map[string]string{
		`<tml version="1"><row><html/></row></tml>`: "tml",
		`<tml version="1"><row><td/></row></tml>`:   "column",
		`<tml version="1"><row><body/></row></tml>`: "code",
	} {
		_, problems, _ := Check([]byte(src), nil)
		if len(problems) == 0 || problems[0].DidYouMean == bad {
			t.Errorf("%s suggests %q", src, bad)
		}
		if s := problems[0].DidYouMean; s != "" && !slices.Contains(problems[0].Accepted, s) {
			t.Errorf("%s suggests %q, which is not accepted there", src, s)
		}
	}
}

// Medium 12: array indexes took a sign.
func TestReviewStrictIndexes(t *testing.T) {
	for _, p := range []string{`[{"op":"remove","path":"/a/+1"}]`, `[{"op":"remove","path":"/a/-0"}]`, `[{"op":"test","path":"/a/ 1","value":2}]`} {
		ops, err := DecodePatch([]byte(p), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := applyPatch(map[string]any{"a": []any{1.0, 2.0}}, 0, ops, DefaultLimits()); err == nil {
			t.Errorf("%s applied", p)
		}
	}
}

// Medium 13: the pointer skipped dropped strings.
func TestReviewPointerMatchesSource(t *testing.T) {
	_, problems, _ := Check([]byte(`{"type":"tml","version":1,"children":["",{"type":"bogus"}]}`), nil)
	if len(problems) == 0 || problems[0].At != "/children/1" {
		t.Errorf("got %v, want /children/1", problems)
	}
}

// The dependency of a path stops at a computed index.
func TestReviewDynamicIndexDeps(t *testing.T) {
	e, _ := ParseExpr("state.items[state.i].name")
	want := []Dep{{"state", "/items"}, {"state", "/i"}}
	if got := e.Deps(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("deps %v, want %v", got, want)
	}
}

// API: the catalog cannot be changed through what it returns.
func TestReviewCatalogCopies(t *testing.T) {
	all := Components()
	all[0].Attrs[0].Name = "changed"
	all[0].Name = "changed"
	if Components()[0].Name == "changed" || Components()[0].Attrs[0].Name == "changed" {
		t.Error("a caller changed the catalog through Components")
	}
	c, _ := Lookup("table")
	c.Attrs[0].Name = "changed"
	c.Children[0] = "changed"
	r := Roles()
	r[0] = "changed"
	again, _ := Lookup("table")
	if again.Attrs[0].Name == "changed" || again.Children[0] == "changed" || Roles()[0] == "changed" {
		t.Error("a caller changed the catalog")
	}
	if AttrType(200).String() == "" {
		t.Error("AttrType.String of an unknown value")
	}
}

// Sanitize removes the wider set of invisible and reordering characters.
func TestReviewSanitizeWide(t *testing.T) {
	for _, r := range []rune{0x061c, 0x180e, 0x200b, 0x200e, 0x200f, 0x2028, 0x2029, 0x202e, 0x2060, 0x2064, 0x2066, 0xfeff, 0xe0001, 0xe007f} {
		if got := Sanitize("a" + string(r) + "b"); got != "ab" {
			t.Errorf("U+%04X survived: %q", r, got)
		}
	}
	for _, keep := range []string{"a\u200db", "a\u200cb", "tab\tnew\nline"} {
		if Sanitize(keep) != keep {
			t.Errorf("%q changed", keep)
		}
	}
	doc, err := ParseXML([]byte(`<tml version="1" title="a&#10;b&#9;c"><input name="x" value="v"/><textarea name="t" value="1&#10;2"/></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc.AttrString("title") != "a b c" || doc.Children[1].AttrString("value") != "1\n2" {
		t.Errorf("one-line attributes: %q, multiline: %q", doc.AttrString("title"), doc.Children[1].AttrString("value"))
	}
}
