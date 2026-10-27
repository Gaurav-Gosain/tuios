package tml

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// applyPatchByFullCopy is applyPatch as it was when every patch began with a
// copy of the whole document. It is the reference the path copy must match.
func applyPatchByFullCopy(doc any, size int, ops []Op, lim Limits) (any, int, error) {
	cur := deepCopy(doc)
	for i, op := range ops {
		next, nsize, err := applyOp(cur, size, op, lim)
		if err != nil {
			var pe *PatchError
			if errors.As(err, &pe) {
				pe.Index = i
				return nil, 0, pe
			}
			return nil, 0, &PatchError{i, CodeBadValue, err.Error()}
		}
		cur, size = next, nsize
	}
	return cur, size, nil
}

// TestApplyPatchMatchesTheFullCopy checks that applyPatch gives the result,
// size and error the full copy gives, and that no patch changes a state an
// earlier patch handed out.
func TestApplyPatchMatchesTheFullCopy(t *testing.T) {
	const seed = 20261026
	lim := DefaultLimits()

	jsonOf := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%#v", v)
		}
		return string(b)
	}

	// call runs one apply and turns a panic into an error, so the report
	// still names the patch.
	call := func(apply func(any, int, []Op, Limits) (any, int, error), doc any, size int, ops []Op) (res any, n int, panicked any, err error) {
		defer func() {
			if p := recover(); p != nil {
				res, n, panicked, err = nil, 0, p, nil
			}
		}()
		res, n, err = apply(doc, size, ops, lim)
		return res, n, nil, err
	}

	// differ describes how the two applies differ, or returns "".
	differ := func(doc any, size int, ops []Op) (string, any, error) {
		wantDoc, wantSize, wantPanic, wantErr := call(applyPatchByFullCopy, doc, size, ops)
		gotDoc, gotSize, gotPanic, gotErr := call(applyPatch, doc, size, ops)
		switch {
		case wantPanic != nil:
			return fmt.Sprintf("the full copy panicked: %v", wantPanic), nil, nil
		case gotPanic != nil:
			return fmt.Sprintf("applyPatch panicked: %v", gotPanic), nil, nil
		case (gotErr == nil) != (wantErr == nil):
			return fmt.Sprintf("error %v, want %v", gotErr, wantErr), nil, nil
		case gotErr != nil:
			var gp, wp *PatchError
			gi, wi := -2, -2
			if errors.As(gotErr, &gp) {
				gi = gp.Index
			}
			if errors.As(wantErr, &wp) {
				wi = wp.Index
			}
			if gotErr.Error() != wantErr.Error() || gi != wi {
				return fmt.Sprintf("error %q (Index %d), want %q (Index %d)", gotErr, gi, wantErr, wi), nil, nil
			}
			if gotDoc != nil || gotSize != wantSize {
				return fmt.Sprintf("on an error the result is %s, size %d; want nil, %d", jsonOf(gotDoc), gotSize, wantSize), nil, nil
			}
			return "", nil, gotErr
		case !reflect.DeepEqual(gotDoc, wantDoc):
			return fmt.Sprintf("result %s\nwant   %s", jsonOf(gotDoc), jsonOf(wantDoc)), nil, nil
		case gotSize != wantSize:
			return fmt.Sprintf("size %d, want %d (result %s)", gotSize, wantSize, jsonOf(gotDoc)), nil, nil
		}
		return "", gotDoc, nil
	}

	// chain applies the patches next gives, each to the previous result,
	// and checks every patch against the full copy. A deep snapshot of each
	// state is taken when it appears; after the chain every state, the
	// input of a failed patch included, must still equal its snapshot.
	chain := func(t *testing.T, label string, doc any, n int, next func(cur any, k int) []Op) (applied, failed int) {
		t.Helper()
		cur := doc
		states, snaps := []any{cur}, []any{deepCopy(cur)}
		var patches []string
		for k := range n {
			ops := next(cur, k)
			patches = append(patches, jsonOf(ops))
			size := cappedSize(cur, lim.StateBytes)
			diff, res, err := differ(cur, size, ops)
			if diff != "" {
				t.Fatalf("%s, patch %d: %s\npatch %s\ndoc   %s", label, k, diff, jsonOf(ops), jsonOf(cur))
			}
			if err != nil {
				failed++
				continue
			}
			applied++
			cur = res
			states, snaps = append(states, cur), append(snaps, deepCopy(cur))
		}
		for i := range states {
			if !reflect.DeepEqual(states[i], snaps[i]) {
				t.Fatalf("%s: state %d changed after the chain\ngot  %s\nwant %s\npatches %s",
					label, i, jsonOf(states[i]), jsonOf(snaps[i]), strings.Join(patches, "\n        "))
			}
		}
		return applied, failed
	}

	fixed := func(patches ...[]Op) func(any, int) []Op {
		return func(_ any, k int) []Op { return patches[k] }
	}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }

	keys := []string{"", "a", "b", "c", "~", "/", "~/", "a/b", "~0", "~1", "0", "1", "-"}
	floats := []float64{0, 1, -1, 7, 42, 4999, 123456789, 1e21, 1e300, math.MaxFloat64,
		0.25, 0.1, -3.5, 1.0 / 3, 5e-324, 1e-7, math.Copysign(0, -1)}
	stringsIn := []string{"", "x", "a longer string", `q"uote`, `back\slash`, "~/"}

	var value func(r *rand.Rand, depth int) any
	value = func(r *rand.Rand, depth int) any {
		kind := r.IntN(8)
		if depth <= 1 {
			kind = r.IntN(4)
		}
		switch kind {
		case 0:
			return stringsIn[r.IntN(len(stringsIn))]
		case 1:
			return floats[r.IntN(len(floats))]
		case 2:
			return r.IntN(2) == 0
		case 3:
			return nil
		case 4, 5:
			m := map[string]any{}
			for range r.IntN(5) {
				m[keys[r.IntN(len(keys))]] = value(r, depth-1)
			}
			return m
		}
		n := r.IntN(5)
		a := make([]any, n, n+r.IntN(3))
		for i := range a {
			a[i] = value(r, depth-1)
		}
		return a
	}

	type node struct {
		path []string
		v    any
	}
	var walk func(v any, path []string, out *[]node)
	walk = func(v any, path []string, out *[]node) {
		*out = append(*out, node{path, v})
		switch c := v.(type) {
		case map[string]any:
			// Sorted, so a seed draws the same patches on every run.
			for _, k := range slices.Sorted(maps.Keys(c)) {
				walk(c[k], append(path[:len(path):len(path)], k), out)
			}
		case []any:
			for i, e := range c {
				walk(e, append(path[:len(path):len(path)], strconv.Itoa(i)), out)
			}
		}
	}
	pointer := func(toks []string) string {
		var b strings.Builder
		for _, tok := range toks {
			b.WriteByte('/')
			b.WriteString(escapePointer(tok))
		}
		return b.String()
	}
	child := func(path []string, tok string) string {
		return pointer(append(path[:len(path):len(path)], tok))
	}

	// randomPatch draws a patch of 1 to 12 ops. Each op's paths come from
	// the document as the ops before it left it.
	randomPatch := func(r *rand.Rand, doc any) []Op {
		shadow := doc
		// Three patches in five draw only paths that resolve and tests that
		// pass, so that long patches apply too. The rest draw a path that
		// does not resolve one time in four, and a failing test one in three.
		badPaths := r.IntN(5) < 2
		ops := make([]Op, 1+r.IntN(12))
		for i := range ops {
			var nodes []node
			walk(shadow, nil, &nodes)
			pick := func() node {
				if r.IntN(30) == 0 {
					return nodes[0]
				}
				if len(nodes) > 1 {
					return nodes[1+r.IntN(len(nodes)-1)]
				}
				return nodes[0]
			}
			// existing is a path that is there, or now and then the root.
			existing := func() string { return pointer(pick().path) }
			// invalid is a path that does not resolve.
			invalid := func() string {
				nd := pick()
				switch c := nd.v.(type) {
				case map[string]any:
					switch r.IntN(3) {
					case 0:
						return child(nd.path, "missing")
					case 1:
						return child(nd.path, "missing") + "/deeper"
					}
				case []any:
					switch r.IntN(4) {
					case 0:
						return child(nd.path, strconv.Itoa(len(c)+1))
					case 1:
						return child(nd.path, "-")
					case 2:
						return child(nd.path, "01")
					}
				default:
					if r.IntN(2) == 0 {
						return child(nd.path, "0")
					}
				}
				switch r.IntN(3) {
				case 0:
					return pointer(nd.path) + "/~2"
				case 1:
					return "no-slash"
				}
				return child(nd.path, "-") + "/x"
			}
			// target is where an add may write: a key of an object, an
			// index of an array up to its length, or -.
			target := func() string {
				var containers []node
				for _, nd := range nodes {
					switch nd.v.(type) {
					case map[string]any, []any:
						containers = append(containers, nd)
					}
				}
				if len(containers) == 0 || r.IntN(30) == 0 {
					return ""
				}
				nd := containers[r.IntN(len(containers))]
				if a, ok := nd.v.([]any); ok {
					if r.IntN(3) == 0 {
						return child(nd.path, "-")
					}
					return child(nd.path, strconv.Itoa(r.IntN(len(a)+1)))
				}
				return child(nd.path, keys[r.IntN(len(keys))])
			}
			path := func(valid func() string) string {
				if badPaths && r.IntN(4) == 0 {
					return invalid()
				}
				return valid()
			}
			val := func() json.RawMessage {
				b, err := json.Marshal(value(r, 3))
				if err != nil {
					panic(err)
				}
				return b
			}
			var op Op
			switch k := r.IntN(14); {
			case k < 4:
				op = Op{Op: "add", Path: path(target), Value: val()}
			case k < 7:
				op = Op{Op: "replace", Path: path(existing), Value: val()}
			case k < 9:
				op = Op{Op: "remove", Path: path(existing)}
			case k < 11:
				op = Op{Op: "copy", From: path(existing), Path: path(target)}
			case k < 13:
				op = Op{Op: "test", Path: path(existing)}
				toks, perr := splitPointer(op.Path)
				if v, err := get(shadow, toks); perr == nil && err == nil && (!badPaths || r.IntN(3) > 0) {
					b, err := json.Marshal(v)
					if err != nil {
						panic(err)
					}
					op.Value = b
				} else {
					op.Value = val()
				}
			default:
				op = Op{Op: "move", From: path(existing), Path: path(target)}
			}
			ops[i] = op
			if next, _, err := applyPatchByFullCopy(shadow, 0, []Op{op}, lim); err == nil {
				shadow = next
			}
		}
		return ops
	}

	t.Run("random patches", func(t *testing.T) {
		const states = 1334
		applied, failed := 0, 0
		for si := range states {
			r := rand.New(rand.NewPCG(seed, uint64(si)))
			doc := map[string]any{}
			for range 1 + r.IntN(5) {
				doc[keys[r.IntN(len(keys))]] = value(r, 4)
			}
			a, f := chain(t, fmt.Sprintf("seed %d, state %d", seed, si), doc, 3, func(cur any, _ int) []Op {
				return randomPatch(r, cur)
			})
			applied, failed = applied+a, failed+f
		}
		t.Logf("%d patches: %d applied, %d failed", applied+failed, applied, failed)
	})

	t.Run("a typed nil map on the path", func(t *testing.T) {
		input := func() map[string]any { return map[string]any{"form": map[string]any(nil), "n": 1.0} }
		doc := input()
		ops := []Op{{Op: "add", Path: "/form/x", Value: raw(`"v"`)}}
		diff, _, err := differ(doc, cappedSize(doc, lim.StateBytes), ops)
		if diff != "" {
			t.Fatalf("%s\npatch %s", diff, jsonOf(ops))
		}
		if err != nil {
			t.Fatalf("the patch failed: %v", err)
		}
		if !reflect.DeepEqual(doc, input()) {
			t.Fatalf("the input changed: %#v", doc)
		}
	})

	t.Run("a store over typed nil containers", func(t *testing.T) {
		input := func() map[string]any {
			return map[string]any{"form": map[string]any(nil), "list": []any(nil), "n": 1.0}
		}
		state := input()
		s, err := NewStore(state, nil)
		if err != nil {
			t.Fatal(err)
		}
		first := []Op{{Op: "replace", Path: "/n", Value: raw(`2`)}}
		if _, err := s.Apply(first); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Set("state.form.title", "x"); err != nil {
			t.Fatal(err)
		}
		in := input()
		want, size, err := applyPatchByFullCopy(in, cappedSize(in, lim.StateBytes), first, lim)
		if err != nil {
			t.Fatal(err)
		}
		want, _, err = applyPatchByFullCopy(want, size, []Op{{Op: "add", Path: "/form/title", Value: raw(`"x"`)}}, lim)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s.State(), want) {
			t.Errorf("state %#v\nwant  %#v", s.State(), want)
		}
		if !reflect.DeepEqual(state, input()) {
			t.Errorf("the caller's map changed: %#v", state)
		}
	})

	t.Run("a copy then writes below its source and its target", func(t *testing.T) {
		doc := decode(t, `{"a":{"b":{"c":1}},"arr":[{"k":1}],"n":0}`)
		chain(t, "copy", doc, 2, fixed(
			[]Op{
				{Op: "copy", From: "/a", Path: "/t"},
				{Op: "add", Path: "/a/b/d", Value: raw(`2`)},
				{Op: "add", Path: "/t/b/e", Value: raw(`3`)},
				{Op: "copy", From: "/arr/0", Path: "/arr/-"},
				{Op: "add", Path: "/arr/1/z", Value: raw(`4`)},
				{Op: "add", Path: "/arr/0/w", Value: raw(`5`)},
			},
			[]Op{
				{Op: "copy", From: "/t/b", Path: "/a/b2"},
				{Op: "remove", Path: "/t/b/c"},
				{Op: "replace", Path: "/a/b2/e", Value: raw(`6`)},
				{Op: "remove", Path: "/arr/0/k"},
			},
		))
	})

	t.Run("a move that shifts an index on the path", func(t *testing.T) {
		doc := map[string]any{"arr": []any{1.0, map[string]any{}, map[string]any{"y": 1.0}}}
		chain(t, "move", doc, 1, fixed(
			[]Op{{Op: "move", From: "/arr/0", Path: "/arr/1/x"}},
		))
	})

	t.Run("an insert into an array with spare capacity", func(t *testing.T) {
		arr := make([]any, 3, 3+4)
		arr[0], arr[1], arr[2] = "a", "b", "c"
		doc := map[string]any{"arr": arr}
		chain(t, "spare capacity", doc, 2, fixed(
			[]Op{{Op: "add", Path: "/arr/1", Value: raw(`"x"`)}},
			[]Op{{Op: "add", Path: "/arr/0", Value: raw(`"y"`)}, {Op: "remove", Path: "/arr/2"}},
		))
	})

	t.Run("float sizes", func(t *testing.T) {
		xs := append([]float64{}, floats...)
		xs = append(xs, 2, 10, 100, 1e15, 1e20, 1<<53, -123456, 0.5, 1.5, 3.14159, -0.001, 1e-300,
			math.SmallestNonzeroFloat64, -math.MaxFloat64, 2.5e-10, 9007199254740993)
		r := rand.New(rand.NewPCG(seed, 0))
		for range 200 {
			xs = append(xs, (r.Float64()-0.5)*math.Pow(10, float64(r.IntN(40)-20)), float64(r.IntN(1e6)))
		}
		for _, x := range xs {
			if got, want := cappedSize(x, lim.StateBytes), len(strconv.FormatFloat(x, 'g', -1, 64)); got != want {
				t.Errorf("cappedSize(%v) = %d, want %d", x, got, want)
			}
		}
	})
}

// TestStoreSetBudget bounds the allocations of one set on a 200-row state.
func TestStoreSetBudget(t *testing.T) {
	rows := make([]any, 200)
	for i := range rows {
		rows[i] = map[string]any{
			"pkg":   fmt.Sprintf("internal/pkg%03d", i%37),
			"test":  fmt.Sprintf("TestCase%04d", i),
			"ms":    float64((i * 7919) % 5000),
			"score": float64(i) + 0.25,
			"line":  fmt.Sprintf("assertion %d failed at line %d", i, i*3),
		}
	}
	s, err := NewStore(map[string]any{"passed": 412.0, "failed": 200.0, "rows": rows}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("passed", 413.0); err != nil {
		t.Fatal(err)
	}
	values := [2]float64{412, 413}
	k := 0
	var setErr error
	allocs := testing.AllocsPerRun(50, func() {
		k++
		if _, err := s.Set("passed", values[k%2]); err != nil && setErr == nil {
			setErr = err
		}
	})
	if setErr != nil {
		t.Fatal(setErr)
	}
	t.Logf("one set on a 200-row state: %.0f allocations", allocs)
	if allocs > 80 {
		t.Errorf("one set on a 200-row state made %.0f allocations; the budget is 80", allocs)
	}
}
