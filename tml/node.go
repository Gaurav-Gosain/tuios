package tml

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Pos is a place in the source of a document. Line and Col count from 1. Col
// counts bytes, as Go error positions do. A zero Pos means the node was built
// in code and has no source.
type Pos struct {
	Line   int `json:"line"`
	Col    int `json:"col"`
	Offset int `json:"-"`
}

// Kind is the type of an attribute value.
type Kind uint8

// The kinds of attribute value. String, Number and Bool are the scalar kinds
// both encodings share. Array and Object hold JSON: the root's state, and a
// literal array in a data attribute such as sparkline values.
const (
	KindString Kind = iota
	KindNumber
	KindBool
	KindArray
	KindObject
)

// Value is an attribute value. A Number keeps its literal text, so a document
// prints back as it was written: 1.50 stays 1.50. An Array or Object keeps its
// JSON in compact form, with the keys in the order they were written.
type Value struct {
	Kind Kind
	Str  string          // String, and the literal text of a Number
	Bool bool            // Bool
	Raw  json.RawMessage // Array and Object, compact
}

// StringValue returns a string Value.
func StringValue(s string) Value { return Value{Kind: KindString, Str: s} }

// NumberValue returns a number Value with the shortest text for f.
func NumberValue(f float64) Value {
	return Value{Kind: KindNumber, Str: strconv.FormatFloat(f, 'f', -1, 64)}
}

// BoolValue returns a boolean Value.
func BoolValue(b bool) Value { return Value{Kind: KindBool, Bool: b} }

// Text returns the value as it is written in an XML attribute.
func (v Value) Text() string {
	switch v.Kind {
	case KindBool:
		return strconv.FormatBool(v.Bool)
	case KindArray, KindObject:
		return string(v.Raw)
	default:
		return v.Str
	}
}

// Float returns a Number as a float64. The second result is false for any
// other kind, and for a number that does not parse.
func (v Value) Float() (float64, bool) {
	if v.Kind != KindNumber {
		return 0, false
	}
	f, err := strconv.ParseFloat(v.Str, 64)
	return f, err == nil
}

// Attr is one attribute of a node, in the order the source wrote it.
type Attr struct {
	Name  string
	Value Value
	Pos   Pos
}

// Node is one node of a TML tree. A node is either a component, with a Type,
// attributes and children, or a text run, with an empty Type and Text.
//
// A component whose Type is not in the catalog is kept as it was written. The
// validator reports it, with a suggestion. Nothing is ever dropped silently.
type Node struct {
	Type     string
	Attrs    []Attr
	Children []*Node
	Text     string
	Pos      Pos

	// src is one more than the node's index in the JSON children list it
	// came from, or 0. Empty strings and adjacent strings are merged, so the
	// tree index can differ from the source index.
	src int
}

// IsText reports whether n is a text run.
func (n *Node) IsText() bool { return n.Type == "" }

// TextNode returns a text run.
func TextNode(s string) *Node { return &Node{Text: s} }

// Attr returns the value of the named attribute.
func (n *Node) Attr(name string) (Value, bool) {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return Value{}, false
}

// AttrString returns the named attribute as text, or "" when it is absent.
func (n *Node) AttrString(name string) string {
	v, ok := n.Attr(name)
	if !ok {
		return ""
	}
	return v.Text()
}

// SetAttr sets the named attribute, keeping its place when it exists.
func (n *Node) SetAttr(name string, v Value) {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			n.Attrs[i].Value = v
			return
		}
	}
	n.Attrs = append(n.Attrs, Attr{Name: name, Value: v})
}

// ID returns the node's id attribute.
func (n *Node) ID() string { return n.AttrString("id") }

// Walk calls fn for n and every node below it, depth first, with the JSON
// Pointer of each node relative to n. When fn returns false, Walk skips the
// children of that node.
func (n *Node) Walk(fn func(node *Node, pointer string) bool) {
	walk(n, "", fn)
}

func walk(n *Node, ptr string, fn func(*Node, string) bool) {
	if !fn(n, ptr) {
		return
	}
	for i, c := range n.Children {
		walk(c, ptr+"/children/"+strconv.Itoa(i), fn)
	}
}

// Find returns the node with the given id, and its JSON Pointer.
func (n *Node) Find(id string) (*Node, string) {
	var found *Node
	var at string
	n.Walk(func(c *Node, ptr string) bool {
		if found != nil {
			return false
		}
		if !c.IsText() && c.ID() == id {
			found, at = c, ptr
			return false
		}
		return true
	})
	return found, at
}

// At returns the node a JSON Pointer of the form /children/0/children/2
// names, or nil.
func (n *Node) At(pointer string) *Node {
	toks, err := splitPointer(pointer)
	if err != nil || len(toks)%2 != 0 {
		return nil
	}
	cur := n
	for i := 0; i < len(toks); i += 2 {
		if toks[i] != "children" {
			return nil
		}
		idx, err := strconv.Atoi(toks[i+1])
		if err != nil || idx < 0 || idx >= len(cur.Children) {
			return nil
		}
		cur = cur.Children[idx]
	}
	return cur
}

// Clone returns a deep copy of n.
func (n *Node) Clone() *Node {
	c := *n
	c.Attrs = make([]Attr, len(n.Attrs))
	for i, a := range n.Attrs {
		c.Attrs[i] = a
		if a.Value.Raw != nil {
			c.Attrs[i].Value.Raw = append(json.RawMessage(nil), a.Value.Raw...)
		}
	}
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, ch := range n.Children {
			c.Children[i] = ch.Clone()
		}
	}
	return &c
}

// State returns the decoded state store of a document: the state attribute of
// the root. A document without one has an empty object. Every string and key
// passes through Sanitize, so a JSON escape such as \u001b cannot reach a
// renderer.
func (n *Node) State() (map[string]any, error) {
	v, ok := n.Attr("state")
	if !ok || v.Kind != KindObject {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(v.Raw, &m); err != nil {
		// encoding/json words its errors differently by Go release.
		return nil, fmt.Errorf("the state is not a JSON object of values a float64 can hold")
	}
	if m == nil {
		m = map[string]any{}
	}
	clean, err := SanitizeJSON(m)
	if err != nil {
		return nil, err
	}
	return clean.(map[string]any), nil
}

// Decode returns the JSON value of an array or object attribute, such as a
// literal rows array. The parsers have already passed its strings and keys
// through Sanitize.
func (v Value) Decode() (any, error) {
	if v.Kind != KindArray && v.Kind != KindObject {
		return nil, fmt.Errorf("the value is a %s, not an array or object", v.kindName())
	}
	var out any
	err := json.Unmarshal(v.Raw, &out)
	return out, err
}

func (v Value) kindName() string {
	switch v.Kind {
	case KindNumber:
		return "number"
	case KindBool:
		return "boolean"
	}
	return "string"
}
