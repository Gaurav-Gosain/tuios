package tml

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// FormatXML prints a tree as TML text. Block content puts each child on its
// own line, indented by two spaces. Inline content stays on one line. Raw
// content (code, markdown, diff, mermaid, kbd, state) is printed verbatim.
// White space that the parser would collapse is written as a character
// reference, so ParseXML(FormatXML(n)) equals n.
func FormatXML(n *Node) []byte {
	var b bytes.Buffer
	printXML(&b, n, 0, true)
	b.WriteByte('\n')
	return b.Bytes()
}

// contentOf returns how the children of n print: block, inline or raw.
func contentOf(n *Node) Content {
	if n.Type == "state" {
		return ContentRaw
	}
	if c := lookup(n.Type); c != nil {
		if c.Content == ContentInline || c.Content == ContentRaw {
			return c.Content
		}
	}
	return ContentBlocks
}

func isInlineElem(n *Node) bool {
	c := lookup(n.Type)
	return c != nil && c.Inline && !c.Block
}

func printXML(b *bytes.Buffer, n *Node, indent int, block bool) {
	if block {
		b.WriteString(strings.Repeat("  ", indent))
	}
	b.WriteByte('<')
	b.WriteString(n.Type)
	var state *Value
	for i := range n.Attrs {
		a := &n.Attrs[i]
		// Only the document root prints its state as an element: the root
		// is the one node printed at indent 0 as a block.
		if a.Name == "state" && n.Type == "tml" && a.Value.Kind == KindObject && indent == 0 && block {
			state = &a.Value
			continue
		}
		b.WriteByte(' ')
		b.WriteString(a.Name)
		b.WriteString(`="`)
		b.WriteString(escapeAttr(a.Value.Text()))
		b.WriteByte('"')
	}
	if len(n.Children) == 0 && state == nil {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	switch contentOf(n) {
	case ContentRaw:
		for _, c := range n.Children {
			if c.IsText() {
				b.WriteString(escapeRaw(c.Text))
			} else {
				printXML(b, c, 0, false)
			}
		}
	case ContentInline:
		trim := !isInlineElem(n)
		last := len(n.Children) - 1
		for i, c := range n.Children {
			if c.IsText() {
				b.WriteString(escapeText(c.Text, trim && i == 0, trim && i == last))
			} else {
				printXML(b, c, 0, false)
			}
		}
	default:
		if !block {
			// A block container inside inline content: print it on one
			// line. The parser trims each of its text children, so the
			// ends are escaped.
			for _, c := range n.Children {
				if c.IsText() {
					b.WriteString(escapeText(c.Text, true, true))
				} else {
					printXML(b, c, 0, false)
				}
			}
			break
		}
		b.WriteByte('\n')
		if state != nil {
			b.WriteString(strings.Repeat("  ", indent+1))
			b.WriteString("<state>")
			b.WriteString(escapeRaw(stateText(state.Raw, indent+1)))
			b.WriteString("</state>\n")
		}
		for _, c := range n.Children {
			if c.IsText() {
				b.WriteString(strings.Repeat("  ", indent+1))
				b.WriteString(escapeText(c.Text, true, true))
			} else {
				printXML(b, c, indent+1, true)
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("  ", indent))
	}
	b.WriteString("</")
	b.WriteString(n.Type)
	b.WriteByte('>')
}

// stateText prints the state on one line when it is short, and indented
// under the <state> element when it is not. The parser compacts it again, so
// both read back as the same tree.
func stateText(raw []byte, indent int) string {
	if len(raw) <= 72 {
		return string(raw)
	}
	var out bytes.Buffer
	pad := strings.Repeat("  ", indent)
	if err := json.Indent(&out, raw, pad, "  "); err != nil {
		return string(raw)
	}
	return out.String()
}

func escapeAttr(s string) string {
	if !strings.ContainsAny(s, "&<\"\n\t") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '"':
			b.WriteString("&quot;")
		case '\n':
			b.WriteString("&#10;")
		case '\t':
			b.WriteString("&#9;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeRaw(s string) string {
	if !strings.ContainsAny(s, "&<") {
		return s
	}
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;")
	return r.Replace(s)
}

// escapeText escapes a text run for content the parser collapses. A tab or
// newline becomes a character reference. A space stays literal unless it
// follows another space, or sits at an end the parser trims.
func escapeText(s string, trimStart, trimEnd bool) string {
	var b strings.Builder
	prevSpace := false
	for i, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '\n':
			b.WriteString("&#10;")
		case '\t':
			b.WriteString("&#9;")
		case ' ':
			atStart := i == 0 && trimStart
			atEnd := i == len(s)-1 && trimEnd
			if prevSpace || atStart || atEnd {
				b.WriteString("&#32;")
				prevSpace = false
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
			continue
		default:
			b.WriteRune(r)
		}
		prevSpace = false
	}
	return b.String()
}

// FormatJSON prints a tree in the JSON encoding. With indent set it puts
// one key per line, indented by two spaces.
func FormatJSON(n *Node, indent bool) []byte {
	var b bytes.Buffer
	printJSON(&b, n)
	if !indent {
		return b.Bytes()
	}
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
		return b.Bytes()
	}
	out.WriteByte('\n')
	return out.Bytes()
}

func printJSON(b *bytes.Buffer, n *Node) {
	if n.IsText() {
		writeJSONString(b, n.Text)
		return
	}
	b.WriteString(`{"type":`)
	writeJSONString(b, n.Type)
	for _, a := range n.Attrs {
		b.WriteByte(',')
		writeJSONString(b, a.Name)
		b.WriteByte(':')
		switch a.Value.Kind {
		case KindNumber:
			b.WriteString(a.Value.Str)
		case KindBool:
			b.WriteString(strconv.FormatBool(a.Value.Bool))
		case KindArray, KindObject:
			b.Write(a.Value.Raw)
		default:
			writeJSONString(b, a.Value.Str)
		}
	}
	if len(n.Children) > 0 {
		b.WriteString(`,"children":[`)
		for i, c := range n.Children {
			if i > 0 {
				b.WriteByte(',')
			}
			printJSON(b, c)
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
}

func writeJSONString(b *bytes.Buffer, s string) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Truncate(b.Len() - 1) // Encode adds a newline
}

// Encoding names a TML encoding for Format.
type Encoding uint8

// The encodings Format can print.
const (
	SameEncoding Encoding = iota // the encoding of the input
	TextEncoding                 // TML text
	JSONEncoding                 // TML JSON, indented
)

// Format parses a document in either encoding and prints it in canonical
// form. It does not validate the document.
func Format(src []byte, to Encoding, opts *ParseOptions) ([]byte, error) {
	doc, err := Parse(src, opts)
	if err != nil {
		return nil, err
	}
	if to == JSONEncoding || to == SameEncoding && IsJSON(src) {
		return FormatJSON(doc, true), nil
	}
	return FormatXML(doc), nil
}
