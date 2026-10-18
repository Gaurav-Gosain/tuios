package tml

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The parsers, the expression evaluator and the patch applier take input
// from strangers, so each is a fuzz target. The seeds are the design's
// examples, the error fixtures, and the files under testdata/fuzz.

func addFiles(f *testing.F, patterns ...string) {
	for _, p := range patterns {
		files, _ := filepath.Glob(p)
		for _, file := range files {
			if b, err := os.ReadFile(file); err == nil {
				f.Add(b)
			}
		}
	}
}

// roomy are limits for reading back printed output, which escaping can make
// longer than the input.
func roomy() *ParseOptions {
	l := DefaultLimits()
	l.DocBytes = 64 << 20
	return &ParseOptions{Limits: &l}
}

// checkTree asserts what every parsed tree must hold: no control or bidi
// override characters, and the depth and node limits.
func checkTree(t *testing.T, doc *Node) {
	lim := DefaultLimits()
	nodes := 0
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		nodes++
		if depth > lim.Depth {
			t.Fatalf("the tree nests %d deep, past the limit of %d", depth, lim.Depth)
		}
		if Sanitize(n.Text) != n.Text {
			t.Fatalf("a text run holds an unsafe character: %q", n.Text)
		}
		for _, a := range n.Attrs {
			if a.Value.Kind == KindString && Sanitize(a.Value.Str) != a.Value.Str {
				t.Fatalf("attribute %s holds an unsafe character: %q", a.Name, a.Value.Str)
			}
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(doc, 1)
	if nodes > lim.Nodes {
		t.Fatalf("the tree has %d nodes, past the limit of %d", nodes, lim.Nodes)
	}
}

// roundTrip asserts that a parsed tree prints and reads back to itself in
// both encodings, through either one, and that the validator accepts any
// tree without a panic.
func roundTrip(t *testing.T, doc *Node) {
	checkTree(t, doc)
	x1 := FormatXML(doc)
	fromXML, err := ParseXML(x1, roomy())
	if err != nil {
		t.Fatalf("printed TML text does not parse: %v\n%s", err, x1)
	}
	if x2 := FormatXML(fromXML); !bytes.Equal(x1, x2) {
		t.Fatalf("TML text does not round trip:\n%s\n%s", x1, x2)
	}
	j1 := FormatJSON(doc, false)
	fromJSON, err := ParseJSON(j1, roomy())
	if err != nil {
		t.Fatalf("printed JSON does not parse: %v\n%s", err, j1)
	}
	if j2 := FormatJSON(fromJSON, false); !bytes.Equal(j1, j2) {
		t.Fatalf("JSON does not round trip:\n%s\n%s", j1, j2)
	}
	if x3 := FormatXML(fromJSON); !bytes.Equal(x1, x3) {
		t.Fatalf("JSON to TML text differs:\n%s\n%s", x1, x3)
	}
	// JSON to TML text and back gives the same JSON, whatever the source.
	if j3 := FormatJSON(fromXML, false); !bytes.Equal(j1, j3) {
		t.Fatalf("TML text to JSON differs:\n%s\n%s", j1, j3)
	}
	_, _ = Validate(doc, nil)
}

func FuzzParseXML(f *testing.F) {
	addFiles(f, "testdata/examples/*.tml", "testdata/errors/*.tml", "testdata/golden/*.tml")
	f.Fuzz(func(t *testing.T, src []byte) {
		doc, err := ParseXML(src, nil)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || len(e.Problems) == 0 {
				t.Fatalf("a parse error is not an *Error with a problem: %v", err)
			}
			return
		}
		roundTrip(t, doc)
	})
}

func FuzzParseJSON(f *testing.F) {
	addFiles(f, "testdata/examples/*.json", "testdata/errors/*.json", "testdata/golden/*.json")
	f.Fuzz(func(t *testing.T, src []byte) {
		// tml's scanner and encoding/json agree on what is JSON.
		if ok := scanJSON(src) == nil; ok != json.Valid(src) {
			t.Fatalf("scanJSON says %v and json.Valid says %v for %q", ok, !ok, src)
		}
		doc, err := ParseJSON(src, nil)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || len(e.Problems) == 0 {
				t.Fatalf("a parse error is not an *Error with a problem: %v", err)
			}
			return
		}
		roundTrip(t, doc)
	})
}

func FuzzExpr(f *testing.F) {
	for _, s := range []string{
		"pct(state.passed, state.passed + state.failed)", "dur(value, 'ms')", "len(state.failures)",
		"state.failures[state.i].test ?? 'none'", "join(pick(state.failures, 'test'), ', ')",
		"fmt(state.bytes, 2) + ' ' + bytes(state.bytes)", "ago(state.at)", "{a: [1, 2], b: !state.ok}",
		"count(state.failures, 'state', 'failed') > 0 ? 'red' : 'green'", "round(avg(state.secs), 1)",
		"state.secs + state.secs + state.secs", "'x' + 'y' + 'z'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		e, err := ParseExpr(src)
		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) || se.Offset < 0 || se.Offset > len(src) {
				t.Fatalf("bad syntax error %v", err)
			}
			return
		}
		env := testEnv()
		env.Budget = NewBudget()
		v, err := e.Eval(env)
		if err != nil && !errors.Is(err, ErrBudget) {
			t.Fatalf("Eval returned %v; only ErrBudget is allowed", err)
		}
		if s, ok := v.(string); ok && len(s) > maxString {
			t.Fatalf("a string of %d bytes, past the limit of %d", len(s), maxString)
		}
		if tmpl, err := ParseTemplate("{" + src + "}"); err == nil {
			env.Budget = NewBudget()
			_ = tmpl.Render(env)
		}
	})
}

func FuzzPatch(f *testing.F) {
	f.Add([]byte(`{"foo":["bar","baz"],"n":1}`), []byte(`[{"op":"add","path":"/foo/1","value":"qux"}]`))
	f.Add([]byte(`{"a":{"b":1}}`), []byte(`[{"op":"move","from":"/a","path":"/c"},{"op":"test","path":"/c/b","value":1}]`))
	f.Add([]byte(`{"a":[1,2,3]}`), []byte(`[{"op":"remove","path":"/a/0"},{"op":"copy","from":"/a","path":"/a/-"}]`))
	f.Add([]byte(`{"~":1}`), []byte(`[{"op":"replace","path":"/~0","value":{"x":[]}}]`))
	f.Fuzz(func(t *testing.T, state, patch []byte) {
		var doc map[string]any
		if json.Unmarshal(state, &doc) != nil || doc == nil {
			return
		}
		s, err := NewStore(doc, nil)
		if err != nil {
			return
		}
		ops, err := DecodePatch(patch, nil)
		if err != nil {
			return
		}
		before := deepCopy(s.State())
		if _, err := s.Apply(ops); err != nil {
			if !reflect.DeepEqual(before, s.State()) {
				t.Fatal("a failed patch changed the state")
			}
			return
		}
		if s.Size() > DefaultLimits().StateBytes {
			t.Fatalf("the state grew to %d bytes", s.Size())
		}
	})
}
