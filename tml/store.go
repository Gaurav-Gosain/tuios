package tml

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Store is the JSON state store of one surface. Every change goes through a
// JSON Patch, from the owner or from a local action, so a renderer learns
// which paths changed and redraws only what depends on them.
//
// A Store is not safe for use by several goroutines.
type Store struct {
	root    map[string]any
	version uint64
	size    int
	lim     Limits
}

// NewStore returns a store holding state. state may be nil.
func NewStore(state map[string]any, lim Limits) (*Store, error) {
	if state == nil {
		state = map[string]any{}
	}
	s := &Store{root: state, lim: lim, size: jsonSize(state)}
	if s.size > lim.StateBytes {
		return nil, fmt.Errorf("the state is %d bytes; the limit is %d bytes", s.size, lim.StateBytes)
	}
	return s, nil
}

// StoreFor returns the store of a document's state.
func StoreFor(doc *Node, lim Limits) (*Store, error) {
	st, err := doc.State()
	if err != nil {
		return nil, err
	}
	return NewStore(st, lim)
}

// State returns the state. The caller must not change it.
func (s *Store) State() map[string]any { return s.root }

// Version counts the changes applied. It starts at 0.
func (s *Store) Version() uint64 { return s.version }

// Size is the state's size as compact JSON.
func (s *Store) Size() int { return s.size }

// Get returns the value at a JSON Pointer, or nil.
func (s *Store) Get(pointer string) any {
	toks, err := splitPointer(pointer)
	if err != nil {
		return nil
	}
	v, err := get(s.root, toks)
	if err != nil {
		return nil
	}
	return v
}

// Apply applies a JSON Patch. It is atomic: on an error the state is
// unchanged. It returns the JSON Pointers the patch changed, for
// Deps.Affected.
func (s *Store) Apply(ops []Op) ([]string, error) {
	next, err := ApplyPatch(s.root, ops)
	if err != nil {
		return nil, err
	}
	m, ok := next.(map[string]any)
	if !ok {
		return nil, &PatchError{0, CodeBadValue, "the state must stay a JSON object"}
	}
	size := jsonSize(m)
	if size > s.lim.StateBytes {
		return nil, &PatchError{-1, CodeLimit, fmt.Sprintf("the state would be %d bytes; the limit is %d bytes", size, s.lim.StateBytes)}
	}
	s.root, s.size = m, size
	s.version++
	var changed []string
	for _, op := range ops {
		switch op.Op {
		case "test":
		case "move":
			changed = append(changed, op.From, op.Path)
		default:
			changed = append(changed, op.Path)
		}
	}
	return changed, nil
}

// Set writes value at a state path such as state.form.title or form.title,
// creating missing objects on the way. It is the set action.
func (s *Store) Set(path string, value any) ([]string, error) {
	ptr, err := PathPointer(path)
	if err != nil {
		return nil, err
	}
	toks, _ := splitPointer(ptr)
	var ops []Op
	cur := any(s.root)
	for i := 0; i < len(toks)-1; i++ {
		next, err := get(cur, toks[i:i+1])
		if err != nil {
			raw, _ := json.Marshal(map[string]any{})
			ops = append(ops, Op{Op: "add", Path: "/" + joinPointer(toks[:i+1]), Value: raw})
			cur = map[string]any{}
			continue
		}
		cur = next
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	ops = append(ops, Op{Op: "add", Path: ptr, Value: raw})
	if len(toks) == 0 {
		return nil, fmt.Errorf("set needs a path below state")
	}
	return s.Apply(ops)
}

func joinPointer(toks []string) string {
	out := make([]string, len(toks))
	for i, t := range toks {
		out[i] = escapePointer(t)
	}
	return strings.Join(out, "/")
}

// PathPointer turns a state path into a JSON Pointer: state.a.b[0] and a.b[0]
// both become /a/b/0. Only names and constant indexes are allowed.
func PathPointer(path string) (string, error) {
	e, err := ParseExpr(path)
	if err != nil {
		return "", fmt.Errorf("%q is not a path: %v", path, err)
	}
	root, ptr, ok := staticPath(e.root)
	if !ok || len(e.deps) != 1 || hasDynamicIndex(e.root) {
		return "", fmt.Errorf("%q is not a path of names and constant indexes", path)
	}
	if root != "state" {
		ptr = "/" + escapePointer(root) + ptr
	}
	if ptr == "" {
		return "", fmt.Errorf("%q names the whole state; name a path below it", path)
	}
	return ptr, nil
}

func hasDynamicIndex(n exprNode) bool {
	switch x := n.(type) {
	case *memberNode:
		return hasDynamicIndex(x.x)
	case *indexNode:
		if _, ok := x.idx.(*litNode); !ok {
			return true
		}
		return hasDynamicIndex(x.x)
	case *pathNode:
		return false
	}
	return true
}

// jsonSize returns the length of v as compact JSON.
func jsonSize(v any) int {
	switch x := v.(type) {
	case nil:
		return 4
	case bool:
		if x {
			return 4
		}
		return 5
	case float64:
		return len(strconv.FormatFloat(x, 'g', -1, 64))
	case string:
		b, _ := json.Marshal(x)
		return len(b)
	case []any:
		n := 2
		for i, e := range x {
			if i > 0 {
				n++
			}
			n += jsonSize(e)
		}
		return n
	case map[string]any:
		n := 2
		i := 0
		for k, e := range x {
			if i > 0 {
				n++
			}
			b, _ := json.Marshal(k)
			n += len(b) + 1 + jsonSize(e)
			i++
		}
		return n
	}
	b, _ := json.Marshal(v)
	return len(b)
}
