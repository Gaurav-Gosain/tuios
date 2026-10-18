package tml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseOptions changes how a document is parsed. The zero value uses
// DefaultLimits.
type ParseOptions struct {
	Limits *Limits
}

func (o *ParseOptions) limits() Limits {
	if o == nil {
		return (*Limits)(nil).resolve()
	}
	return o.Limits.resolve()
}

// Parse parses a document in either encoding. A document whose first
// character, after white space, is { is JSON. Anything else is TML text.
func Parse(src []byte, opts *ParseOptions) (*Node, error) {
	if IsJSON(src) {
		return ParseJSON(src, opts)
	}
	return ParseXML(src, opts)
}

// IsJSON reports whether src looks like the JSON encoding.
func IsJSON(src []byte) bool {
	t := bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	t = bytes.TrimLeft(t, " \t\r\n")
	return len(t) > 0 && t[0] == '{'
}

// ParseXML parses TML text: a strict XML subset. It rejects a DTD, entities
// other than the five standard ones and numeric references, namespaces,
// CDATA and processing instructions. Comments are allowed and dropped.
//
// The tree it returns is canonical: attribute values are typed by the
// catalog, white space in text is collapsed as described in the README, and
// the <state> element of the root becomes the root's state attribute.
func ParseXML(src []byte, opts *ParseOptions) (*Node, error) {
	lim := opts.limits()
	if len(src) > lim.DocBytes {
		return nil, limitError(fmt.Sprintf("The document is %d bytes. The limit is %d bytes.", len(src), lim.DocBytes))
	}
	p := &xmlParser{src: src, lim: lim}
	p.pos = 0
	if bytes.HasPrefix(src, []byte("\xef\xbb\xbf")) {
		p.pos = 3
	}
	root, err := p.document()
	if err != nil {
		return nil, err
	}
	return root, nil
}

func limitError(msg string) *Error {
	return &Error{Problems: []Problem{{Code: CodeLimit, Message: msg}}}
}

type xmlParser struct {
	src   []byte
	pos   int
	lim   Limits
	nodes int
	lines []int
}

// rawChild is a child before the content model of its parent applies.
type rawChild struct {
	node  *Node
	parts []string // raw source text, entities not yet decoded, joined once
	pos   Pos
}

func (p *xmlParser) posAt(off int) Pos {
	if p.lines == nil {
		p.lines = lineStarts(p.src)
	}
	return posFromLines(p.lines, off)
}

// lineStarts returns the offset of the first byte of each line.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func posFromLines(starts []int, off int) Pos {
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > off }) - 1
	if i < 0 {
		i = 0
	}
	return Pos{Line: i + 1, Col: off - starts[i] + 1, Offset: off}
}

func (p *xmlParser) fail(off int, code, format string, args ...any) *Error {
	pos := p.posAt(off)
	return &Error{Problems: []Problem{{Line: pos.Line, Col: pos.Col, Code: code, Message: fmt.Sprintf(format, args...)}}}
}

func (p *xmlParser) eof() bool { return p.pos >= len(p.src) }

func (p *xmlParser) hasPrefix(s string) bool { return bytes.HasPrefix(p.src[p.pos:], []byte(s)) }

func (p *xmlParser) skipSpace() {
	for !p.eof() && isXMLSpace(p.src[p.pos]) {
		p.pos++
	}
}

func isXMLSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// misc skips white space and comments, and rejects what TML leaves out.
func (p *xmlParser) misc() error {
	for {
		p.skipSpace()
		switch {
		case p.hasPrefix("<!--"):
			if err := p.comment(); err != nil {
				return err
			}
		case p.hasPrefix("<?"), p.hasPrefix("<!"):
			return p.unsupported()
		default:
			return nil
		}
	}
}

func (p *xmlParser) unsupported() error {
	switch {
	case p.hasPrefix("<?"):
		return p.fail(p.pos, CodeUnsupportedXML, "TML text has no processing instructions or XML declaration. Remove the <?...?> line.")
	case p.hasPrefix("<![CDATA["):
		return p.fail(p.pos, CodeUnsupportedXML, "TML text has no CDATA sections. Escape < as &lt; and & as &amp; instead.")
	default:
		return p.fail(p.pos, CodeUnsupportedXML, "TML text has no DTD or entity declarations.")
	}
}

func (p *xmlParser) comment() error {
	start := p.pos
	end := bytes.Index(p.src[p.pos+4:], []byte("-->"))
	if end < 0 {
		return p.fail(start, CodeSyntax, "The comment has no end. Close it with -->.")
	}
	p.pos += 4 + end + 3
	return nil
}

func (p *xmlParser) document() (*Node, error) {
	if err := p.misc(); err != nil {
		return nil, err
	}
	if p.eof() || p.src[p.pos] != '<' {
		return nil, p.fail(p.pos, CodeSyntax, "The document must start with an element, such as <tml version=\"1\">.")
	}
	root, err := p.element(1)
	if err != nil {
		return nil, err
	}
	if root.Type == "state" {
		return nil, p.fail(0, CodeMisplacedState, "<state> must be the first child of the <tml> root, and appear once.")
	}
	stateLast(root)
	if err := p.misc(); err != nil {
		return nil, err
	}
	if !p.eof() {
		return nil, p.fail(p.pos, CodeSyntax, "Text after the root element. A document has one root element.")
	}
	return root, nil
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c == '-' || c == '.' || (c >= '0' && c <= '9')
}

// validName reports whether s is a legal component or attribute name.
func validName(s string) bool {
	if s == "" || len(s) > 64 || !isNameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isNameChar(s[i]) {
			return false
		}
	}
	return true
}

func (p *xmlParser) name() (string, error) {
	start := p.pos
	if p.eof() || !isNameStart(p.src[p.pos]) {
		return "", p.fail(p.pos, CodeSyntax, "Expected a name here.")
	}
	for !p.eof() && (isNameChar(p.src[p.pos]) || p.src[p.pos] == ':') {
		if p.src[p.pos] == ':' {
			return "", p.fail(p.pos, CodeUnsupportedXML, "TML text has no namespaces. Remove the prefix before ':'.")
		}
		p.pos++
	}
	if p.pos-start > 64 {
		return "", p.fail(start, CodeSyntax, "The name is longer than 64 characters.")
	}
	return string(p.src[start:p.pos]), nil
}

func (p *xmlParser) element(depth int) (*Node, error) {
	start := p.pos
	if depth > p.lim.Depth {
		return nil, p.fail(start, CodeLimit, "The document nests deeper than %d levels.", p.lim.Depth)
	}
	p.nodes++
	if p.nodes > p.lim.Nodes {
		return nil, p.fail(start, CodeLimit, "The document has more than %d nodes.", p.lim.Nodes)
	}
	p.pos++ // <
	typ, err := p.name()
	if err != nil {
		return nil, err
	}
	n := &Node{Type: typ, Pos: p.posAt(start)}
	seen := map[string]bool{}
	for {
		hadSpace := !p.eof() && isXMLSpace(p.src[p.pos])
		p.skipSpace()
		if p.eof() {
			return nil, p.fail(start, CodeSyntax, "<%s> has no end.", typ)
		}
		if p.hasPrefix("/>") {
			p.pos += 2
			return n, nil
		}
		if p.src[p.pos] == '>' {
			p.pos++
			break
		}
		if !hadSpace {
			return nil, p.fail(p.pos, CodeSyntax, "Put a space between attributes.")
		}
		aoff := p.pos
		an, err := p.name()
		if err != nil {
			return nil, err
		}
		if an == "type" {
			if bytes.HasPrefix(p.src[p.pos:], []byte(`="password"`)) {
				return nil, p.fail(aoff, CodePasswordRefused, "TML has no password input in version 1. Ask for secrets outside the document.")
			}
			return nil, p.fail(aoff, CodeSyntax, "type is the element name in TML text. Remove the type attribute.")
		}
		if an == "xmlns" {
			return nil, p.fail(aoff, CodeUnsupportedXML, "TML text has no namespaces. Remove xmlns.")
		}
		if seen[an] {
			return nil, p.fail(aoff, CodeSyntax, "<%s> has the attribute %s twice.", typ, an)
		}
		seen[an] = true
		p.skipSpace()
		if p.eof() || p.src[p.pos] != '=' {
			return nil, p.fail(p.pos, CodeSyntax, "The attribute %s needs a value: %s=\"...\".", an, an)
		}
		p.pos++
		p.skipSpace()
		if p.eof() || (p.src[p.pos] != '"' && p.src[p.pos] != '\'') {
			return nil, p.fail(p.pos, CodeSyntax, "Put the value of %s in quotes.", an)
		}
		q := p.src[p.pos]
		p.pos++
		vstart := p.pos
		end := bytes.IndexByte(p.src[p.pos:], q)
		if end < 0 {
			return nil, p.fail(vstart-1, CodeSyntax, "The value of %s has no closing quote.", an)
		}
		rawVal := p.src[vstart : vstart+end]
		if i := bytes.IndexByte(rawVal, '<'); i >= 0 {
			return nil, p.fail(vstart+i, CodeSyntax, "Write < as &lt; inside an attribute value.")
		}
		val, err := p.decode(rawVal, vstart)
		if err != nil {
			return nil, err
		}
		p.pos = vstart + end + 1
		tv, err := typedValue(typ, an, Sanitize(val))
		if err != nil {
			return nil, p.fail(aoff, CodeBadValue, "The value of %s is not valid: %v.", an, err)
		}
		n.Attrs = append(n.Attrs, Attr{Name: an, Value: tv, Pos: p.posAt(aoff)})
	}

	// Content.
	var kids []rawChild
	for {
		if p.eof() {
			return nil, p.fail(start, CodeSyntax, "<%s> is not closed. Add </%s>.", typ, typ)
		}
		switch {
		case p.hasPrefix("</"):
			coff := p.pos
			p.pos += 2
			cn, err := p.name()
			if err != nil {
				return nil, err
			}
			if cn != typ {
				return nil, p.fail(coff, CodeSyntax, "</%s> closes <%s>, which is still open. Close <%s> first.", cn, typ, typ)
			}
			p.skipSpace()
			if p.eof() || p.src[p.pos] != '>' {
				return nil, p.fail(p.pos, CodeSyntax, "Expected > to end </%s>.", cn)
			}
			p.pos++
			if err := p.finish(n, kids, depth); err != nil {
				return nil, err
			}
			return n, nil
		case p.hasPrefix("<!--"):
			if err := p.comment(); err != nil {
				return nil, err
			}
		case p.hasPrefix("<?"), p.hasPrefix("<!"):
			return nil, p.unsupported()
		case p.src[p.pos] == '<':
			c, err := p.element(depth + 1)
			if err != nil {
				return nil, err
			}
			kids = append(kids, rawChild{node: c})
		default:
			toff := p.pos
			end := bytes.IndexByte(p.src[p.pos:], '<')
			if end < 0 {
				end = len(p.src) - p.pos
			}
			text := string(p.src[p.pos : p.pos+end])
			p.pos += end
			// Text split by comments is one run. The pieces are joined once
			// in finish, so many comments cost linear time.
			if len(kids) > 0 && kids[len(kids)-1].node == nil {
				kids[len(kids)-1].parts = append(kids[len(kids)-1].parts, text)
			} else {
				kids = append(kids, rawChild{parts: []string{text}, pos: p.posAt(toff)})
			}
		}
	}
}

// finish applies the content model of n to its raw children.
func (p *xmlParser) finish(n *Node, kids []rawChild, depth int) error {
	comp := lookup(n.Type)
	content := ContentBlocks
	inlineElem := false
	if comp != nil {
		content, inlineElem = comp.Content, comp.Inline && !comp.Block
	}
	if n.Type == "state" {
		content = ContentRaw
	}
	merged := kids
	for i, k := range merged {
		if k.node != nil {
			n.Children = append(n.Children, k.node)
			continue
		}
		raw := strings.Join(k.parts, "")
		var text string
		switch content {
		case ContentRaw:
			text = raw
		case ContentInline:
			text = collapse(raw)
			if !inlineElem {
				if i == 0 {
					text = strings.TrimLeft(text, " ")
				}
				if i == len(merged)-1 {
					text = strings.TrimRight(text, " ")
				}
			}
		default:
			text = strings.Trim(collapse(raw), " ")
		}
		if text == "" {
			continue
		}
		dec, err := p.decode([]byte(text), k.pos.Offset)
		if err != nil {
			return err
		}
		dec = Sanitize(dec)
		if dec == "" {
			continue
		}
		p.nodes++
		if p.nodes > p.lim.Nodes {
			return p.fail(k.pos.Offset, CodeLimit, "The document has more than %d nodes.", p.lim.Nodes)
		}
		n.Children = append(n.Children, &Node{Text: dec, Pos: k.pos})
	}
	// The JSON form has one children key, so a node cannot hold both a
	// children attribute (tree's field name) and child nodes.
	if _, has := n.Attr("children"); has && len(n.Children) > 0 {
		return p.fail(n.Pos.Offset, CodeSyntax, "<%s> has a children attribute and child nodes. Keep one.", n.Type)
	}
	if n.Type == "tml" && depth == 1 {
		if err := p.absorbState(n); err != nil {
			return err
		}
	}
	for _, c := range n.Children {
		if c.Type == "state" {
			return p.fail(c.Pos.Offset, CodeMisplacedState, "<state> must be the first child of the <tml> root, and appear once.")
		}
	}
	return nil
}

// absorbState turns the root's first <state> element into the state
// attribute. A later or nested <state> stays a node, and the validator
// reports it.
func (p *xmlParser) absorbState(root *Node) error {
	for i, c := range root.Children {
		if _, has := root.Attr("state"); has {
			return nil
		}
		if c.IsText() {
			continue
		}
		if c.Type != "state" {
			return nil
		}
		src := ""
		for _, t := range c.Children {
			if !t.IsText() {
				return p.fail(t.Pos.Offset, CodeSyntax, "<state> holds JSON text only.")
			}
			src += t.Text
		}
		if strings.TrimSpace(src) == "" {
			src = "{}"
		}
		raw, err := canonicalJSON([]byte(src))
		if err != nil {
			return p.fail(c.Pos.Offset, CodeSyntax, "<state> is not valid: %v.", err)
		}
		if len(raw) == 0 || raw[0] != '{' {
			return p.fail(c.Pos.Offset, CodeBadValue, "<state> must hold a JSON object, such as {\"count\": 0}.")
		}
		root.Attrs = append(root.Attrs, Attr{Name: "state", Value: Value{Kind: KindObject, Raw: raw}, Pos: c.Pos})
		root.Children = append(root.Children[:i:i], root.Children[i+1:]...)
		// Text on both sides of <state> is now one run.
		if i > 0 && i < len(root.Children) && root.Children[i-1].IsText() && root.Children[i].IsText() {
			root.Children[i-1].Text += root.Children[i].Text
			root.Children = append(root.Children[:i:i], root.Children[i+1:]...)
		}
		return nil
	}
	return nil
}

// collapse turns each run of literal white space into one space.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(s); i++ {
		if isXMLSpace(s[i]) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteByte(s[i])
	}
	return b.String()
}

func entity(name string) (string, bool) {
	switch name {
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "amp":
		return "&", true
	case "quot":
		return "\"", true
	case "apos":
		return "'", true
	}
	return "", false
}

// decode replaces entity and character references.
func (p *xmlParser) decode(raw []byte, off int) (string, error) {
	if bytes.IndexByte(raw, '&') < 0 {
		return string(raw), nil
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '&' {
			b.WriteByte(raw[i])
			continue
		}
		end := bytes.IndexByte(raw[i:], ';')
		if end < 0 || end > 12 {
			return "", p.fail(off+i, CodeSyntax, "Write & as &amp;.")
		}
		ref := string(raw[i+1 : i+end])
		if strings.HasPrefix(ref, "#") {
			var r int64
			var err error
			if strings.HasPrefix(ref, "#x") || strings.HasPrefix(ref, "#X") {
				r, err = strconv.ParseInt(ref[2:], 16, 32)
			} else {
				r, err = strconv.ParseInt(ref[1:], 10, 32)
			}
			if err != nil || r <= 0 || r > utf8.MaxRune || (r >= 0xd800 && r <= 0xdfff) {
				return "", p.fail(off+i, CodeSyntax, "&%s; is not a valid character reference.", ref)
			}
			b.WriteRune(rune(r))
		} else if e, ok := entity(ref); ok {
			b.WriteString(e)
		} else {
			return "", p.fail(off+i, CodeUnsupportedXML, "&%s; is not allowed. Use &lt; &gt; &amp; &quot; &apos; or a numeric reference such as &#10;.", ref)
		}
		i += end
	}
	return b.String(), nil
}

// typedValue gives an attribute its canonical value from its text, by the
// type the catalog gives it. Both parsers use it, so the two encodings of
// one tree are equal.
func typedValue(typ, name, text string) (Value, error) {
	comp := lookup(typ)
	if comp == nil {
		return StringValue(text), nil
	}
	spec, ok := comp.Spec(name)
	if !ok {
		return StringValue(text), nil
	}
	switch spec.Type {
	case TString:
		if !spec.Multiline {
			return StringValue(oneLine(text)), nil
		}
	case TInt, TNumber:
		if isJSONNumber(text) {
			return Value{Kind: KindNumber, Str: text}, nil
		}
	case TBool:
		if text == "true" || text == "false" {
			return BoolValue(text == "true"), nil
		}
	case TData, TObject:
		open := byte('[')
		kind := KindArray
		if spec.Type == TObject {
			open, kind = '{', KindObject
		}
		if strings.HasPrefix(text, string(open)) && json.Valid([]byte(text)) {
			// A literal array or object is data a renderer shows, so its
			// strings and keys are sanitized here, once.
			raw, err := canonicalJSON([]byte(text))
			if err != nil {
				return Value{}, err
			}
			return Value{Kind: kind, Raw: raw}, nil
		}
	}
	return StringValue(text), nil
}

// isJSONNumber reports whether s is a number in JSON syntax.
func isJSONNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}
	if s[i] == '0' {
		i++
	} else if s[i] >= '1' && s[i] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	} else {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		d := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == d {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		d := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == d {
			return false
		}
	}
	return i == len(s)
}

// oneLine turns the tabs and line breaks of a one-line text attribute into
// spaces.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// stateLast moves the root's state attribute to the end of its attributes.
// TML text writes state as an element after the tag, so this is its one
// canonical place, and both encodings agree on the order.
func stateLast(root *Node) {
	if root.Type != "tml" {
		return
	}
	for i, a := range root.Attrs {
		if a.Name == "state" && i != len(root.Attrs)-1 {
			root.Attrs = append(append(root.Attrs[:i:i], root.Attrs[i+1:]...), a)
			return
		}
	}
}
