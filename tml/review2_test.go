package tml

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// These tests hold the findings of the verification of the wave 1 fixes.
// Each case has a deadline: an input that once took seconds must now take
// well under one.

const deadline = 200 * time.Millisecond

func within(t *testing.T, name string, f func()) {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	// A case that hangs fails here instead of at the test binary's timeout.
	// Its goroutine keeps running until the test binary exits.
	select {
	case <-done:
	case <-time.After(20 * deadline):
		t.Fatalf("%s is still running after %v", name, 20*deadline)
	}
	if took := time.Since(start); took > deadline {
		t.Errorf("%s took %v, past the %v deadline", name, took, deadline)
	}
}

func nested(depth int) string {
	return strings.Repeat(`{"k":`, depth) + `"\u001b"` + strings.Repeat(`}`, depth)
}

// Critical: SanitizeValue sanitized a dirty child twice per level. 24
// levels around one ESC took 3.5 s.
func TestVerifyDeepSanitize(t *testing.T) {
	for _, depth := range []int{40, 5000} {
		v := nested(depth)
		within(t, fmt.Sprintf("patch at depth %d", depth), func() {
			s, _ := NewStore(nil, nil)
			ops, err := DecodePatch([]byte(`[{"op":"add","path":"/x","value":`+v+`}]`), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Apply(ops); err != nil {
				t.Fatal(err)
			}
		})
		within(t, fmt.Sprintf("Check at depth %d", depth), func() {
			if _, _, err := Check([]byte(`<tml version="1"><state>{"x":`+v+`}</state></tml>`), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	e, err := ParseExpr(strings.Repeat("{k:", 40) + "state.e" + strings.Repeat("}", 40))
	if err != nil {
		t.Fatal(err)
	}
	within(t, "Eval of an object literal at depth 40", func() {
		if _, err := e.Eval(&Env{Vars: map[string]any{"state": map[string]any{"e": "\x1b"}}}); err != nil {
			t.Fatal(err)
		}
	})
}

// High: literal data arrays kept JSON escapes.
func TestVerifyLiteralData(t *testing.T) {
	for _, src := range []string{
		`<tml version="1"><table rows='[{"n":"\u001b[2J"}]'><column field="n"/></table></tml>`,
		`{"type":"tml","version":1,"children":[{"type":"table","rows":[{"n\u001b":"\u001b[2J\u202e"}],"children":[{"type":"column","field":"n"}]}]}`,
		`<tml version="1"><select name="s" options='["\u001b]52;c;eA==\u0007"]'/></tml>`,
	} {
		doc, err := Parse([]byte(src), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range doc.Children[0].Attrs {
			if a.Value.Kind != KindArray {
				continue
			}
			v, err := a.Value.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if s := Stringify(v); strings.ContainsAny(s, "\x1b\u0007\u202e") {
				t.Errorf("%s kept an escape: %q", a.Name, s)
			}
		}
	}
}

// join charged nothing per element: 200,000 nulls joined freely.
func TestVerifyJoinCharged(t *testing.T) {
	s, err := NewStore(map[string]any{"z": make([]any, 200000)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := ParseExpr("[" + strings.TrimSuffix(strings.Repeat("join(state.z,''),", 230), ",") + "]")
	within(t, "join over nulls", func() {
		if _, err := e.Eval(&Env{Vars: map[string]any{"state": s.State()}}); !errors.Is(err, ErrBudget) {
			t.Errorf("got %v, want ErrBudget", err)
		}
	})
}

// The sanitize scan of a result was free, so one large state string could
// be scanned for every binding of a frame.
func TestVerifyResultScanCharged(t *testing.T) {
	s, err := NewStore(map[string]any{"s": strings.Repeat("x", 1000000)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"state.s", "[state.s,state.s,state.s,state.s]", "{a: state.s}"} {
		e, _ := ParseExpr(src)
		b := NewBudget()
		n := 0
		within(t, src+" until the frame budget is spent", func() {
			for n < 100000 {
				if _, err := e.Eval(&Env{Vars: map[string]any{"state": s.State()}, Budget: b}); err != nil {
					break
				}
				n++
			}
		})
		if n > 10 {
			t.Errorf("%s: %d bindings fit in one frame budget", src, n)
		}
	}
}

// NewStore took Go values outside the JSON model unmeasured and unsanitized.
func TestVerifyTypedValues(t *testing.T) {
	for _, bad := range []map[string]any{
		{"rows": []map[string]any{{"n": "\x1b[2J"}}},
		{"strs": []string{"\x1b]52;c;eA==\x07", strings.Repeat("A", 50<<20)}},
		{"m": map[string]string{"\x1b": "\x1b[31m"}},
	} {
		if _, err := NewStore(bad, nil); err == nil {
			t.Errorf("NewStore took %T values", bad)
		}
	}
	s, _ := NewStore(map[string]any{"i": 3, "f": float32(0.5)}, nil)
	if s.Get("/i") != 3.0 || s.Get("/f") != 0.5 {
		t.Errorf("int and float32 were not turned into float64: %v", s.State())
	}
	if _, err := s.Set("state.x", []string{"\x1b[2J"}); err != nil || Stringify(s.Get("/x")) != `["[2J"]` {
		t.Errorf("Set of a []string: %v %q", err, Stringify(s.Get("/x")))
	}
	e, _ := ParseExpr("item")
	if v, _ := e.Eval(&Env{Vars: map[string]any{"item": []string{"\x1b[2J"}}}); v != nil {
		t.Errorf("a []string Env value read as %#v, want null", v)
	}
}

// Keys that became one after Sanitize kept a value chosen by map order.
func TestVerifyKeyCollision(t *testing.T) {
	m := map[string]any{"a": 1.0, "a\u200b": 2.0, "a\u202e": 3.0}
	if _, err := NewStore(m, nil); err == nil {
		t.Error("NewStore took colliding keys")
	}
	_, problems, err := Check([]byte(`<tml version="1"><state>{"a":1,"a\u200b":2}</state></tml>`), nil)
	if err == nil {
		t.Errorf("Check took colliding state keys: %v", problems)
	}
	ops, _ := DecodePatch([]byte(`[{"op":"add","path":"/p","value":{"b":1,"b\u2066":2}}]`), nil)
	s, _ := NewStore(nil, nil)
	if _, err := s.Apply(ops); err == nil {
		t.Error("a patch took colliding keys")
	}
	for range 50 {
		if got := SanitizeValue(m).(map[string]any); len(got) != 1 || got["a"] != 1.0 {
			t.Fatalf("SanitizeValue is not deterministic: %v", got)
		}
	}
}

// A patch could grow an each array past the limit after validation.
func TestVerifyPatchExpansion(t *testing.T) {
	doc, _, err := Check([]byte(`<tml version="1"><state>{"a":[1,2]}</state><col each="state.a"><text>{item}</text></col></tml>`), nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := StoreFor(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	ops, _ := DecodePatch([]byte(`[{"op":"replace","path":"/a","value":[`+strings.TrimSuffix(strings.Repeat("0,", 3000), ",")+`]}]`), nil)
	var pe *PatchError
	if _, err := s.Apply(ops); !errors.As(err, &pe) || pe.Code != CodeLimit {
		t.Errorf("a patch past the each limit applied: %v", err)
	}
	if len(s.State()["a"].([]any)) != 2 {
		t.Error("the refused patch changed the state")
	}
}

// Rows counted per table, not across tables and each.
func TestVerifyRowsAcrossEach(t *testing.T) {
	big := "[" + strings.Repeat("{},", 40000) + "{}]"
	small := "[" + strings.Repeat("0,", 599) + "0]"
	for name, body := range map[string]string{
		"tables":     `<table rows="state.b"><column field="x"/></table>`,
		"sparklines": `<sparkline values="state.b"/>`,
	} {
		src := `<tml version="1"><state>{"a":` + small + `,"b":` + big + `}</state><col each="state.a">` + body + `</col></tml>`
		_, problems, err := Check([]byte(src), nil)
		if err == nil || problems[len(problems)-1].Code != CodeLimit {
			t.Errorf("%s: want a limit problem, got %v", name, problems)
		}
	}
	arr := "[" + strings.Repeat("["+strings.TrimSuffix(strings.Repeat("0,", 40), ",")+"],", 39) + "[0]]"
	for _, body := range []string{
		`<col each="state.a" as="r"><col each="r"><text>x</text></col></col>`,
		`<col each="state.a"><col each="item"><text>x</text></col></col>`,
		`<list items="state.a"><col each="item"><text>x</text></col></list>`,
	} {
		src := `<tml version="1"><state>{"a":` + arr + `}</state>` + body + `</tml>`
		if _, problems, err := Check([]byte(src), nil); err == nil {
			t.Errorf("%s: 39 x 40 expanded nodes passed: %v", body, problems)
		}
	}
}

// Zero values: a zero Limits field means its default, and a zero Store
// works.
func TestVerifyZeroValues(t *testing.T) {
	var z Limits
	if _, _, err := Check([]byte(`<tml version="1"><text>hi</text></tml>`), &ValidateOptions{Limits: &z}); err != nil {
		t.Errorf("zero Limits refused a small document: %v", err)
	}
	if _, err := NewStore(nil, &z); err != nil {
		t.Errorf("NewStore with zero Limits: %v", err)
	}
	var s Store
	if _, err := s.Set("state.a", 1); err != nil || s.Get("/a") != 1.0 {
		t.Errorf("zero Store Set: %v", err)
	}
	if _, err := Validate(nil, nil); err == nil {
		t.Error("Validate(nil) passed")
	}
	if _, err := (&Expr{}).Eval(nil); err != nil {
		t.Errorf("zero Expr: %v", err)
	}
}

// No escape survives anywhere a renderer or a log can read it.
func TestVerifyNoEscapeAnywhere(t *testing.T) {
	for _, src := range []string{
		`{"type":"tml","version":1,"children":[{"type":"text","title":"\u001b[2J","children":["\u001b[31mred"]}]}`,
		`{"type":"tml","version":1,"children":[{"type":"text","id":"a\u001b"}]}`,
		`<tml version="1"><button on-press="emit('&#27;[2J', 'x&#155;')">b</button></tml>`,
		"<tml version=\"1\"><text>{'\x1b[2J'}</text></tml>",
		`{"type":"tml","version":1,"children":[{"type":"\u001b[2Jblink"}]}`,
		`{"type":"tml","version":1,"children":[{"type":"text","\u001b[31mattr":"x"}]}`,
	} {
		doc, problems, err := Check([]byte(src), nil)
		if err != nil {
			for _, p := range problems {
				if strings.ContainsAny(p.String(), "\x1b\u009b") {
					t.Errorf("a problem holds an escape: %q", p.String())
				}
			}
		}
		if doc == nil {
			continue
		}
		doc.Walk(func(n *Node, _ string) bool {
			text := n.Type + n.Text
			for _, a := range n.Attrs {
				text += a.Name + a.Value.Text()
				if acts, err := ParseActions(a.Value.Text()); err == nil && strings.HasPrefix(a.Name, "on-") {
					for _, act := range acts {
						for i := range act.Args {
							text += act.Literal(i)
						}
					}
				}
			}
			if strings.ContainsAny(text, "\x1b\u009b") {
				t.Errorf("an escape survived in %q", text)
			}
			return true
		})
	}
}

// Deep and long expressions parse and evaluate in bounded time.
func TestVerifyExprShapes(t *testing.T) {
	for name, src := range map[string]string{
		"brackets": strings.Repeat("[", 4000),
		"parens":   strings.Repeat("(", 2000) + "1" + strings.Repeat(")", 2000),
		"neg":      strings.Repeat("-", 4000) + "1",
		"cond":     strings.Repeat("1?1:", 1000) + "1",
		"objects":  strings.Repeat("{a:", 1000) + "1" + strings.Repeat("}", 1000),
		"plus":     strings.Repeat("1+", 2000) + "1",
		"members":  "state" + strings.Repeat(".a", 2000),
	} {
		within(t, name, func() {
			if e, err := ParseExpr(src); err == nil {
				_, _ = e.Eval(nil)
			}
		})
	}
}
