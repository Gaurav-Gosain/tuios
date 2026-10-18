package tml

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Store is the JSON state store of one surface. Every change goes through a
// JSON Patch, from the owner or from a local action, and returns the paths
// it changed, so a renderer can redraw only what depends on them. Every
// string and key that enters the store passes through Sanitize.
//
// A Store is not safe for use by several goroutines.
type Store struct {
	root    map[string]any
	version uint64
	size    int
	lim     Limits
	doc     *Node // the tree whose each, list and data the patches may grow
}

// NewStore returns a store holding state. state may be nil. The store keeps
// a sanitized copy when state holds an unsafe character.
func NewStore(state map[string]any, limits *Limits) (*Store, error) {
	lim := limits.resolve()
	if state == nil {
		state = map[string]any{}
	}
	v, err := SanitizeJSON(state)
	if err != nil {
		return nil, fmt.Errorf("the state is not valid: %v", err)
	}
	clean := v.(map[string]any)
	s := &Store{root: clean, lim: lim, size: cappedSize(clean, lim.StateBytes)}
	if s.size > lim.StateBytes {
		return nil, fmt.Errorf("the state is over %d bytes, the limit", lim.StateBytes)
	}
	return s, nil
}

// StoreFor returns the store of a document's state. The store keeps the
// tree, and Apply refuses a patch that would make each, list, the images or
// the rows of tables, lists, trees and charts pass their limits.
func StoreFor(doc *Node, lim *Limits) (*Store, error) {
	st, err := doc.State()
	if err != nil {
		return nil, err
	}
	s, err := NewStore(st, lim)
	if err != nil {
		return nil, err
	}
	s.doc = doc
	return s, nil
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
// unchanged. The size limit is checked after every operation. It returns the
// JSON Pointers the patch changed.
func (s *Store) Apply(ops []Op) ([]string, error) {
	if s.root == nil {
		s.root, s.lim = map[string]any{}, (*Limits)(nil).resolve()
	}
	next, _, err := applyPatch(s.root, s.size, ops, s.lim)
	if err != nil {
		return nil, err
	}
	m, ok := next.(map[string]any)
	if !ok {
		return nil, &PatchError{0, CodeBadValue, "the state must stay a JSON object"}
	}
	size := cappedSize(m, s.lim.StateBytes)
	if size > s.lim.StateBytes {
		return nil, &PatchError{-1, CodeLimit, fmt.Sprintf("the state would pass %d bytes, the limit", s.lim.StateBytes)}
	}
	if s.doc != nil {
		if over := measure(s.doc, m).check(s.lim); len(over) > 0 {
			return nil, &PatchError{-1, CodeLimit, "after the patch: " + over[0]}
		}
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
// creating missing objects on the way. It is the set action. The value is
// sanitized, and a number that is not finite is refused.
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
			ops = append(ops, Op{Op: "add", Path: "/" + joinPointer(toks[:i+1]), Value: json.RawMessage("{}")})
			cur = map[string]any{}
			continue
		}
		cur = next
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("set %s: %v", path, err)
	}
	ops = append(ops, Op{Op: "add", Path: ptr, Value: raw})
	return s.Apply(ops)
}

func joinPointer(toks []string) string {
	out := make([]string, len(toks))
	for i, t := range toks {
		out[i] = escapePointer(t)
	}
	return strings.Join(out, "/")
}

// SplitPath reads a path of names and constant indexes, such as
// state.items[0].name or item.x, into its root name and a JSON Pointer below
// it.
func SplitPath(path string) (root, pointer string, err error) {
	e, err := ParseExpr(path)
	if err != nil {
		return "", "", fmt.Errorf("%q is not a path: %v", path, err)
	}
	root, ptr, dyn, ok := staticPath(e.root)
	if !ok || dyn {
		return "", "", fmt.Errorf("%q is not a path of names and constant indexes", path)
	}
	return root, ptr, nil
}

// PathPointer turns a state path into a JSON Pointer: state.a.b[0] and a.b[0]
// both become /a/b/0. Only names and constant indexes are allowed.
func PathPointer(path string) (string, error) {
	root, ptr, err := SplitPath(path)
	if err != nil {
		return "", err
	}
	if root != "state" {
		ptr = "/" + escapePointer(root) + ptr
	}
	if ptr == "" {
		return "", fmt.Errorf("%q names the whole state; name a path below it", path)
	}
	return ptr, nil
}

// cappedSize returns the length of v as compact JSON, or a number past
// limit as soon as the walk passes it.
func cappedSize(v any, limit int) int {
	n := 0
	var walk func(v any)
	walk = func(v any) {
		if n > limit {
			return
		}
		switch x := v.(type) {
		case nil:
			n += 4
		case bool:
			n += 5
		case float64:
			n += len(strconv.FormatFloat(x, 'g', -1, 64))
		case string:
			n += len(x) + 2 + strings.Count(x, `"`) + strings.Count(x, `\`)
		case []any:
			n += 1 + len(x)
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			n += 1 + len(x)
			for k, e := range x {
				n += len(k) + 3
				walk(e)
			}
		default:
			n += 4
		}
	}
	walk(v)
	return n
}
