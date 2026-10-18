package tml

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testEnv() *Env {
	return &Env{
		Vars: map[string]any{
			"state": map[string]any{
				"passed": 412.0, "failed": 3.0, "name": "api", "ok": true,
				"secs":     []any{41.0, 39.0, 44.0},
				"failures": []any{map[string]any{"test": "A", "ms": 2210.0, "state": "failed"}, map[string]any{"test": "B", "ms": 840.0, "state": "passed"}},
				"bytes":    3328599654.0, "at": 1700000000.0, "nested": map[string]any{"a": map[string]any{"b": 7.0}},
				"i": 1.0,
			},
			"view": map[string]any{"width": 80.0},
		},
		Now: func() time.Time { return time.Unix(1700000000+190, 0) },
	}
}

// TestEval covers each operator and function the design lists.
func TestEval(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		{"1 + 2 * 3", 7.0},
		{"(1 + 2) * 3", 9.0},
		{"7 % 4", 3.0},
		{"1 / 0", nil},
		{"-state.failed", -3.0},
		{"state.passed > 400 && state.failed < 5", true},
		{"state.missing ?? 'none'", "none"},
		{"state.ok ? 'yes' : 'no'", "yes"},
		{"!state.ok", false},
		{"state.name == 'api'", true},
		{"state.name != 'api'", false},
		{"'a' < 'b'", true},
		{"'n=' + state.failed", "n=3"},
		{"state.failures[0].test", "A"},
		{"state.failures[state.i].test", "B"},
		{"state.nested.a.b", 7.0},
		{"state.nested['a']['b']", 7.0},
		{"state.secs[9]", nil},
		{"view.width", 80.0},
		{"[1, 2][1]", 2.0},
		{"len(state.secs)", 3.0},
		{"len('héllo')", 5.0},
		{"sum(state.secs)", 124.0},
		{"sum(state.failures, 'ms')", 3050.0},
		{"avg(state.secs)", 124.0 / 3},
		{"min(state.secs)", 39.0},
		{"max(3, 9, 4)", 9.0},
		{"round(2.567, 2)", 2.57},
		{"round(2.5)", 3.0},
		{"abs(-4)", 4.0},
		{"pct(state.passed, state.passed + state.failed)", 412.0 / 415 * 100},
		{"pct(0.4)", 40.0},
		{"pct(1, 0)", 0.0},
		{"fmt(1234567.891, 2)", "1,234,567.89"},
		{"format(1234.5, 0)", "1,235"},
		{"fmt('text')", "text"},
		{"dur(2210, 'ms')", "2.21s"},
		{"dur(840)", "840ms"},
		{"dur(245, 's')", "4m 5s"},
		{"dur(3840, 's')", "1h 4m"},
		{"dur(0.12, 'ms')", "120µs"},
		{"bytes(state.bytes)", "3.1G"},
		{"bytes(512)", "512B"},
		{"ago(state.at)", "3m ago"},
		{"ago(state.at + 400)", "in 3m"},
		{"upper(state.name)", "API"},
		{"lower('API')", "api"},
		{"trim('  x  ')", "x"},
		{"join(state.secs, '/')", "41/39/44"},
		{"join(pick(state.failures, 'test'))", "A, B"},
		{"contains(state.name, 'p')", true},
		{"contains(state.secs, 39)", true},
		{"starts(state.name, 'ap')", true},
		{"count(state.failures, 'state', 'failed')", 1.0},
		{"count(state.failures)", 2.0},
		{"nosuch(1)", nil},
		{"{a: 1, 'b': state.failed}", map[string]any{"a": 1.0, "b": 3.0}},
	}
	for _, c := range cases {
		e, err := ParseExpr(c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		got, err := e.Eval(testEnv())
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
}

// TestExprSyntax checks that what the language leaves out does not parse.
func TestExprSyntax(t *testing.T) {
	for _, src := range []string{
		"state.a = 1", "1 +", "'open", "state.", "f(1,", "a ? b", "#", "[1, 2",
		strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100),
		strings.Repeat("!", 100) + "x",
		strings.Repeat("x", maxExprLen+1),
	} {
		if _, err := ParseExpr(src); err == nil {
			t.Errorf("%q parsed; it should not", src)
		}
	}
}

// TestBudget checks that an expression stops at the step budget, and that a
// template renders "!" when it does.
func TestBudget(t *testing.T) {
	big := make([]any, 5000)
	for i := range big {
		big[i] = float64(i)
	}
	env := &Env{Vars: map[string]any{"state": map[string]any{"xs": big}}, Budget: NewBudget()}
	e, _ := ParseExpr("sum(state.xs) + sum(state.xs) + sum(state.xs)")
	if _, err := e.Eval(env); !errors.Is(err, ErrBudget) {
		t.Fatalf("got %v, want ErrBudget", err)
	}
	tmpl, _ := ParseTemplate("total {sum(state.xs)}")
	if got := tmpl.Render(env); got != "!" {
		t.Errorf("an exhausted budget renders %q, want !", got)
	}
	env.Budget = NewBudget()
	if got := tmpl.Render(env); got != "total 12497500" {
		t.Errorf("got %q", got)
	}
}

// TestDeps checks the paths an expression records.
func TestDeps(t *testing.T) {
	e, err := ParseExpr("state.a.b + len(state.items[state.i]) + item.x + state['c d'][2]")
	if err != nil {
		t.Fatal(err)
	}
	want := []Dep{{"state", "/a/b"}, {"state", "/items"}, {"state", "/i"}, {"item", "/x"}, {"state", "/c d/2"}}
	if !reflect.DeepEqual(e.Deps(), want) {
		t.Errorf("got %v, want %v", e.Deps(), want)
	}
}

func TestTemplate(t *testing.T) {
	env := testEnv()
	for src, want := range map[string]any{
		"{state.passed}":                412.0,
		"Step: {state.name}!":           "Step: api!",
		"{{literal}} {state.failed}":    "{literal} 3",
		"{ {a: 1}.a }":                  1.0,
		"{state.missing}":               nil,
		"x{'}'}y":                       "x}y",
		"esc {'\x1b[31m' + state.name}": "esc [31mapi",
	} {
		tmpl, err := ParseTemplate(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		got, err := tmpl.Eval(env)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %#v (%v), want %#v", src, got, err, want)
		}
	}
	for _, bad := range []string{"{", "}", "{}", "{state.}", "a } b"} {
		if _, err := ParseTemplate(bad); err == nil {
			t.Errorf("%q parsed; it should not", bad)
		}
	}
}
