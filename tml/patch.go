package tml

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Op is one operation of an RFC 6902 JSON Patch.
type Op struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// PatchError is a patch that cannot apply. Index is the operation that
// failed. A failed test is Code test_failed: the owner reads the state again.
type PatchError struct {
	Index int
	Code  string
	Msg   string
}

func (e *PatchError) Error() string { return fmt.Sprintf("op %d: %s", e.Index, e.Msg) }

// DecodePatch decodes a JSON Patch document and checks its size and shape.
func DecodePatch(src []byte, lim Limits) ([]Op, error) {
	if len(src) > lim.PatchBytes {
		return nil, &PatchError{-1, CodeLimit, fmt.Sprintf("the patch is %d bytes; the limit is %d bytes", len(src), lim.PatchBytes)}
	}
	var ops []Op
	if err := json.Unmarshal(src, &ops); err != nil {
		return nil, &PatchError{-1, CodeSyntax, "the patch is not a JSON array of operations: " + err.Error()}
	}
	for i, op := range ops {
		if err := checkOp(op); err != nil {
			err.Index = i
			return nil, err
		}
	}
	return ops, nil
}

func checkOp(op Op) *PatchError {
	switch op.Op {
	case "add", "replace", "test":
		if op.Value == nil {
			return &PatchError{Code: CodeSyntax, Msg: op.Op + " needs a value"}
		}
	case "move", "copy":
		if _, err := splitPointer(op.From); err != nil {
			return &PatchError{Code: CodeSyntax, Msg: "from: " + err.Error()}
		}
	case "remove":
	default:
		return &PatchError{Code: CodeSyntax, Msg: fmt.Sprintf("%q is not an operation; use add, remove, replace, move, copy or test", op.Op)}
	}
	if _, err := splitPointer(op.Path); err != nil {
		return &PatchError{Code: CodeSyntax, Msg: "path: " + err.Error()}
	}
	return nil
}

// splitPointer splits an RFC 6901 JSON Pointer into its unescaped tokens.
func splitPointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if p[0] != '/' {
		return nil, fmt.Errorf("%q must start with /", p)
	}
	toks := strings.Split(p[1:], "/")
	for i, t := range toks {
		if strings.Contains(t, "~") {
			for j := 0; j < len(t); j++ {
				if t[j] == '~' && (j+1 >= len(t) || (t[j+1] != '0' && t[j+1] != '1')) {
					return nil, fmt.Errorf("%q has a ~ that is not ~0 or ~1", p)
				}
			}
			toks[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
		}
	}
	return toks, nil
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// ApplyPatch applies ops to doc and returns the new document. It is atomic:
// on an error, doc is unchanged and the result is nil. doc must hold JSON
// values as encoding/json decodes them into any.
func ApplyPatch(doc any, ops []Op) (any, error) {
	cur := deepCopy(doc)
	for i, op := range ops {
		next, err := applyOp(cur, op)
		if err != nil {
			var pe *PatchError
			if errors.As(err, &pe) {
				pe.Index = i
				return nil, pe
			}
			return nil, &PatchError{i, CodeBadValue, err.Error()}
		}
		cur = next
	}
	return cur, nil
}

func applyOp(doc any, op Op) (any, error) {
	if err := checkOp(op); err != nil {
		return nil, err
	}
	path, _ := splitPointer(op.Path)
	switch op.Op {
	case "add":
		v, err := decodeValue(op.Value)
		if err != nil {
			return nil, err
		}
		return add(doc, path, v)
	case "remove":
		d, _, err := remove(doc, path)
		return d, err
	case "replace":
		v, err := decodeValue(op.Value)
		if err != nil {
			return nil, err
		}
		if _, err := get(doc, path); err != nil {
			return nil, err
		}
		if len(path) == 0 {
			return v, nil
		}
		d, _, err := remove(doc, path)
		if err != nil {
			return nil, err
		}
		return add(d, path, v)
	case "move":
		from, _ := splitPointer(op.From)
		if isPrefix(from, path) && len(from) < len(path) {
			return nil, &PatchError{Code: CodeBadValue, Msg: "cannot move a value into itself"}
		}
		d, v, err := remove(doc, from)
		if err != nil {
			return nil, err
		}
		return add(d, path, v)
	case "copy":
		from, _ := splitPointer(op.From)
		v, err := get(doc, from)
		if err != nil {
			return nil, err
		}
		return add(doc, path, deepCopy(v))
	case "test":
		v, err := decodeValue(op.Value)
		if err != nil {
			return nil, err
		}
		got, err := get(doc, path)
		if err != nil || !equal(got, v) {
			return nil, &PatchError{Code: "test_failed", Msg: fmt.Sprintf("test at %s failed; read the state again", op.Path)}
		}
		return doc, nil
	}
	return nil, &PatchError{Code: CodeSyntax, Msg: "unknown op"}
}

func decodeValue(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, &PatchError{Code: CodeSyntax, Msg: "the value is not JSON: " + err.Error()}
	}
	return v, nil
}

func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func notFound(path []string) error {
	return &PatchError{Code: "path_missing", Msg: "the state has no /" + strings.Join(path, "/")}
}

func arrayIndex(tok string, n int, allowEnd bool) (int, bool) {
	if allowEnd && tok == "-" {
		return n, true
	}
	if tok == "" || (len(tok) > 1 && tok[0] == '0') {
		return 0, false
	}
	i, err := strconv.Atoi(tok)
	if err != nil || i < 0 {
		return 0, false
	}
	if allowEnd {
		return i, i <= n
	}
	return i, i < n
}

// get returns the value at path.
func get(doc any, path []string) (any, error) {
	cur := doc
	for i, tok := range path {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[tok]
			if !ok {
				return nil, notFound(path[:i+1])
			}
			cur = v
		case []any:
			idx, ok := arrayIndex(tok, len(c), false)
			if !ok {
				return nil, notFound(path[:i+1])
			}
			cur = c[idx]
		default:
			return nil, notFound(path[:i+1])
		}
	}
	return cur, nil
}

// add sets the value at path, inserting into an array. The parent must
// exist. It changes containers in place, so callers pass a copy.
func add(doc any, path []string, v any) (any, error) {
	if len(path) == 0 {
		return v, nil
	}
	parent, err := get(doc, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	switch c := parent.(type) {
	case map[string]any:
		c[last] = v
		return doc, nil
	case []any:
		idx, ok := arrayIndex(last, len(c), true)
		if !ok {
			return nil, notFound(path)
		}
		c = append(c, nil)
		copy(c[idx+1:], c[idx:])
		c[idx] = v
		return setAt(doc, path[:len(path)-1], c)
	}
	return nil, notFound(path)
}

// remove deletes the value at path and returns it.
func remove(doc any, path []string) (any, any, error) {
	if len(path) == 0 {
		return nil, doc, nil
	}
	parent, err := get(doc, path[:len(path)-1])
	if err != nil {
		return nil, nil, err
	}
	last := path[len(path)-1]
	switch c := parent.(type) {
	case map[string]any:
		v, ok := c[last]
		if !ok {
			return nil, nil, notFound(path)
		}
		delete(c, last)
		return doc, v, nil
	case []any:
		idx, ok := arrayIndex(last, len(c), false)
		if !ok {
			return nil, nil, notFound(path)
		}
		v := c[idx]
		c = append(c[:idx:idx], c[idx+1:]...)
		d, err := setAt(doc, path[:len(path)-1], c)
		return d, v, err
	}
	return nil, nil, notFound(path)
}

// setAt replaces the container at path, for an array whose slice header
// changed.
func setAt(doc any, path []string, v any) (any, error) {
	if len(path) == 0 {
		return v, nil
	}
	parent, err := get(doc, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	switch c := parent.(type) {
	case map[string]any:
		c[last] = v
	case []any:
		idx, _ := arrayIndex(last, len(c), false)
		c[idx] = v
	}
	return doc, nil
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = deepCopy(e)
		}
		return a
	}
	return v
}
