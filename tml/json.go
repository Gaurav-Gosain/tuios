package tml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ParseJSON parses the JSON encoding of a document. A node is an object with
// a "type", its attributes beside it, and an optional "children" list in
// which a string is a text run.
//
// The tree it returns is canonical, as ParseXML's is: attribute values are
// typed by the catalog, empty text runs are dropped and adjacent ones joined.
func ParseJSON(src []byte, opts *ParseOptions) (*Node, error) {
	lim := opts.limits()
	if len(src) > lim.DocBytes {
		return nil, limitError(fmt.Sprintf("The document is %d bytes. The limit is %d bytes.", len(src), lim.DocBytes))
	}
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	p := &jsonParser{src: src, lim: lim, dec: json.NewDecoder(bytes.NewReader(src))}
	// Syntax is checked first by tml's own scanner, so a problem's text and
	// position do not depend on the Go release.
	if serr := scanJSON(src); serr != nil {
		return nil, p.fail(serr.off, CodeSyntax, "%s", serr.msg)
	}
	p.dec.UseNumber()
	tok, err := p.dec.Token()
	if err != nil {
		return nil, p.syntax(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, p.fail(0, CodeSyntax, "The document must be a JSON object with a type, such as {\"type\": \"tml\", \"version\": 1}.")
	}
	root, err := p.object(1, p.tokenStart(0))
	if err != nil {
		return nil, err
	}
	stateLast(root)
	if _, err := p.dec.Token(); err != io.EOF {
		return nil, p.fail(int(p.dec.InputOffset()), CodeSyntax, "Text after the root object. A document has one root object.")
	}
	return root, nil
}

type jsonParser struct {
	src   []byte
	dec   *json.Decoder
	lim   Limits
	nodes int
	lines []int
}

func (p *jsonParser) posAt(off int) Pos {
	if p.lines == nil {
		p.lines = lineStarts(p.src)
	}
	return posFromLines(p.lines, off)
}

func (p *jsonParser) fail(off int, code, format string, args ...any) *Error {
	pos := p.posAt(off)
	return &Error{Problems: []Problem{{Line: pos.Line, Col: pos.Col, Code: code, Message: fmt.Sprintf(format, args...)}}}
}

func (p *jsonParser) syntax(err error) *Error {
	var se *json.SyntaxError
	off := int(p.dec.InputOffset())
	if errors.As(err, &se) {
		off = int(se.Offset)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return p.fail(len(p.src), CodeSyntax, "The JSON ends too early. Check for a missing } or ].")
	}
	return p.fail(off, CodeSyntax, "The JSON does not parse here.")
}

// tokenStart returns the offset of the next token after off, past white
// space, commas and colons.
func (p *jsonParser) tokenStart(off int) int {
	for off < len(p.src) {
		switch p.src[off] {
		case ' ', '\t', '\r', '\n', ',', ':':
			off++
		default:
			return off
		}
	}
	return off
}

// object parses a node whose { the decoder has read.
func (p *jsonParser) object(depth, start int) (*Node, error) {
	if depth > p.lim.Depth {
		return nil, p.fail(start, CodeLimit, "The document nests deeper than %d levels.", p.lim.Depth)
	}
	p.nodes++
	if p.nodes > p.lim.Nodes {
		return nil, p.fail(start, CodeLimit, "The document has more than %d nodes.", p.lim.Nodes)
	}
	n := &Node{Pos: p.posAt(start)}
	type pending struct {
		name string
		raw  json.RawMessage
		pos  Pos
	}
	var attrs []pending
	seen := map[string]bool{}
	hasType := false
	for p.dec.More() {
		koff := p.tokenStart(int(p.dec.InputOffset()))
		tok, err := p.dec.Token()
		if err != nil {
			return nil, p.syntax(err)
		}
		key, _ := tok.(string)
		if seen[key] {
			return nil, p.fail(koff, CodeSyntax, "The key %q appears twice in one node.", key)
		}
		seen[key] = true
		voff := p.tokenStart(int(p.dec.InputOffset()))
		switch key {
		case "type":
			var t any
			if err := p.dec.Decode(&t); err != nil {
				return nil, p.syntax(err)
			}
			s, ok := t.(string)
			if !ok || !validName(s) {
				return nil, p.fail(voff, CodeSyntax, "type must be a component name, such as \"card\".")
			}
			if s == "state" {
				return nil, p.fail(voff, CodeMisplacedState, "In JSON, state is an attribute of the tml root: {\"type\": \"tml\", \"state\": {...}}.")
			}
			n.Type, hasType = s, true
		case "children":
			if voff < len(p.src) && p.src[voff] == '[' {
				if err := p.children(n, depth); err != nil {
					return nil, err
				}
				continue
			}
			fallthrough // tree has a children attribute: the field name
		default:
			if !validName(key) {
				return nil, p.fail(koff, CodeSyntax, "%q is not a valid attribute name. Use letters, digits, - and _.", key)
			}
			var raw json.RawMessage
			if err := p.dec.Decode(&raw); err != nil {
				return nil, p.syntax(err)
			}
			attrs = append(attrs, pending{key, raw, p.posAt(koff)})
		}
	}
	if _, err := p.dec.Token(); err != nil { // }
		return nil, p.syntax(err)
	}
	if !hasType {
		return nil, p.fail(start, CodeSyntax, "The node has no type. Add \"type\", such as \"type\": \"text\".")
	}
	for _, a := range attrs {
		v, err := rawToValue(a.raw)
		if err != nil {
			return nil, p.fail(a.pos.Offset, CodeSyntax, "The value of %s is not valid: %v.", a.name, err)
		}
		if v.Kind == KindString {
			v.Str = Sanitize(v.Str)
		}
		tv, err := typedValue(n.Type, a.name, v.Text())
		if err != nil {
			return nil, p.fail(a.pos.Offset, CodeBadValue, "The value of %s is not valid: %v.", a.name, err)
		}
		n.Attrs = append(n.Attrs, Attr{Name: a.name, Value: tv, Pos: a.pos})
	}
	return n, nil
}

// children parses a children list.
func (p *jsonParser) children(n *Node, depth int) error {
	if _, err := p.dec.Token(); err != nil { // [
		return p.syntax(err)
	}
	// Adjacent strings are one text run. They are collected and joined once,
	// so many strings cost linear time. A node keeps its index in the source
	// list, so a problem's pointer matches what the author wrote.
	var pending []string
	pendingIdx, pendingPos := 0, Pos{}
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		p.nodes++
		if p.nodes > p.lim.Nodes {
			return p.fail(pendingPos.Offset, CodeLimit, "The document has more than %d nodes.", p.lim.Nodes)
		}
		n.Children = append(n.Children, &Node{Text: strings.Join(pending, ""), Pos: pendingPos, src: pendingIdx + 1})
		pending = pending[:0]
		return nil
	}
	for idx := 0; p.dec.More(); idx++ {
		off := p.tokenStart(int(p.dec.InputOffset()))
		tok, err := p.dec.Token()
		if err != nil {
			return p.syntax(err)
		}
		switch t := tok.(type) {
		case json.Delim:
			if t != '{' {
				return p.fail(off, CodeSyntax, "A child must be a node object or a string.")
			}
			if err := flush(); err != nil {
				return err
			}
			c, err := p.object(depth+1, off)
			if err != nil {
				return err
			}
			c.src = idx + 1
			n.Children = append(n.Children, c)
		case string:
			s := Sanitize(t)
			if s == "" {
				continue
			}
			if len(pending) == 0 {
				pendingIdx, pendingPos = idx, p.posAt(off)
			}
			pending = append(pending, s)
		default:
			return p.fail(off, CodeSyntax, "A child must be a node object or a string, not %v.", t)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if _, err := p.dec.Token(); err != nil { // ]
		return p.syntax(err)
	}
	return nil
}

// rawToValue turns one JSON value into a Value. null is refused: an absent
// attribute is written by leaving it out.
func rawToValue(raw json.RawMessage) (Value, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Value{}, errors.New("empty")
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return Value{}, err
		}
		return StringValue(s), nil
	case 't', 'f':
		return BoolValue(raw[0] == 't'), nil
	case 'n':
		return Value{}, errors.New("null is not a value; leave the attribute out")
	case '[', '{':
		// C1 controls and bidi overrides may sit unescaped in a JSON
		// string. They are never JSON syntax, so dropping them from the raw
		// bytes keeps the value valid.
		raw = []byte(Sanitize(string(raw)))
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return Value{}, err
		}
		k := KindArray
		if raw[0] == '{' {
			k = KindObject
		}
		return Value{Kind: k, Raw: buf.Bytes()}, nil
	default:
		return Value{Kind: KindNumber, Str: string(raw)}, nil
	}
}
