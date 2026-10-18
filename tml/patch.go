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
	Index   int
	Code    string
	Message string
}

func (e *PatchError) Error() string { return fmt.Sprintf("op %d: %s", e.Index, e.Message) }

// DecodePatch decodes a JSON Patch document and checks its size and shape.
func DecodePatch(src []byte, limits *Limits) ([]Op, error) {
	lim := limits.resolve()
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
			return &PatchError{Code: CodeSyntax, Message: op.Op + " needs a value"}
		}
	case "move", "copy":
		if _, err := splitPointer(op.From); err != nil {
			return &PatchError{Code: CodeSyntax, Message: "from: " + err.Error()}
		}
	case "remove":
	default:
		return &PatchError{Code: CodeSyntax, Message: fmt.Sprintf("%q is not an operation; use add, remove, replace, move, copy or test", op.Op)}
	}
	if _, err := splitPointer(op.Path); err != nil {
		return &PatchError{Code: CodeSyntax, Message: "path: " + err.Error()}
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

// applyPatch applies ops to doc and returns the new document and its size
// as compact JSON. It is atomic: on an error, doc is unchanged and the
// result is nil. size is doc's size. The running size is checked after every
// operation, so a patch of copies cannot grow the state past
// lim.StateBytes before the check runs.
func applyPatch(doc any, size int, ops []Op, lim Limits) (any, int, error) {
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

func overLimit(size int, lim Limits) error {
	if size > lim.StateBytes {
		return &PatchError{Code: CodeLimit, Message: fmt.Sprintf("the state would pass %d bytes, the limit", lim.StateBytes)}
	}
	return nil
}

// entrySize is the size a value adds at path, with its key or comma.
func entrySize(path []string, v any, limit int) int {
	n := cappedSize(v, limit) + 1
	if len(path) > 0 {
		n += len(path[len(path)-1]) + 3
	}
	return n
}

func applyOp(doc any, size int, op Op, lim Limits) (any, int, error) {
	if err := checkOp(op); err != nil {
		return nil, 0, err
	}
	path, _ := splitPointer(op.Path)
	switch op.Op {
	case "add", "replace":
		v, err := decodeValue(op.Value)
		if err != nil {
			return nil, 0, err
		}
		old, getErr := get(doc, path)
		switch {
		case len(path) == 0:
			size = cappedSize(v, lim.StateBytes)
		case op.Op == "replace":
			if getErr != nil {
				return nil, 0, getErr
			}
			size += entrySize(path, v, lim.StateBytes) - entrySize(path, old, lim.StateBytes)
		case getErr == nil && isObjectMember(doc, path):
			size += entrySize(path, v, lim.StateBytes) - entrySize(path, old, lim.StateBytes)
		default: // a new member, or an insert into an array
			size += entrySize(path, v, lim.StateBytes)
		}
		if err := overLimit(size, lim); err != nil {
			return nil, 0, err
		}
		if len(path) == 0 {
			return v, size, nil
		}
		if op.Op == "replace" {
			if doc, _, err = remove(doc, path); err != nil {
				return nil, 0, err
			}
		}
		d, err := add(doc, path, v)
		return d, size, err
	case "remove":
		d, v, err := remove(doc, path)
		if err != nil {
			return nil, 0, err
		}
		return d, size - entrySize(path, v, lim.StateBytes), nil
	case "move":
		from, _ := splitPointer(op.From)
		if isPrefix(from, path) && len(from) < len(path) {
			return nil, 0, &PatchError{Code: CodeBadValue, Message: "cannot move a value into itself"}
		}
		d, v, err := remove(doc, from)
		if err != nil {
			return nil, 0, err
		}
		d, err = add(d, path, v)
		return d, size, err
	case "copy":
		from, _ := splitPointer(op.From)
		v, err := get(doc, from)
		if err != nil {
			return nil, 0, err
		}
		// Measure before copying: a copy of the root doubles the state.
		size += entrySize(path, v, lim.StateBytes)
		if err := overLimit(size, lim); err != nil {
			return nil, 0, err
		}
		d, err := add(doc, path, deepCopy(v))
		return d, size, err
	case "test":
		v, err := decodeValue(op.Value)
		if err != nil {
			return nil, 0, err
		}
		got, err := get(doc, path)
		if err != nil || !equal(got, v) {
			return nil, 0, &PatchError{Code: CodeTestFailed, Message: fmt.Sprintf("test at %s failed; read the state again", op.Path)}
		}
		return doc, size, nil
	}
	return nil, 0, &PatchError{Code: CodeSyntax, Message: "unknown op"}
}

func isObjectMember(doc any, path []string) bool {
	if len(path) == 0 {
		return true
	}
	parent, err := get(doc, path[:len(path)-1])
	_, isObj := parent.(map[string]any)
	return err == nil && isObj
}

// decodeValue decodes a patch value, with every string and key passed
// through Sanitize.
func decodeValue(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, &PatchError{Code: CodeSyntax, Message: "the value is not JSON: " + err.Error()}
	}
	clean, err := SanitizeJSON(v)
	if err != nil {
		return nil, &PatchError{Code: CodeBadValue, Message: err.Error()}
	}
	return clean, nil
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
	return &PatchError{Code: CodePathMissing, Message: "the state has no /" + strings.Join(path, "/")}
}

// arrayIndex reads an RFC 6901 array index: 0, or digits with no leading
// zero. A sign is not allowed.
func arrayIndex(tok string, n int, allowEnd bool) (int, bool) {
	if allowEnd && tok == "-" {
		return n, true
	}
	if tok == "" || len(tok) > 9 || (len(tok) > 1 && tok[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return 0, false
		}
	}
	i, err := strconv.Atoi(tok)
	if err != nil {
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
