package raster

import (
	"errors"
	"strconv"
	"strings"
)

// node is one element of the SVG, with its attributes in order and its children.
// Text content is held in text nodes, whose name is "".
type node struct {
	name     string
	attrs    [][2]string
	text     string
	children []*node
}

func (n *node) attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if a[0] == name {
			return a[1], true
		}
	}
	return "", false
}

// maxDepth bounds the nesting of elements, so that hostile markup cannot run the
// drawing out of stack.
const maxDepth = 256

// parseXML reads the subset of XML that pele writes: elements with quoted
// attributes, character data and the five entities plus numeric references.
// Comments, processing instructions and CDATA are skipped.
func parseXML(s string) (*node, error) {
	root := &node{}
	stack := []*node{root}
	i := 0
	for i < len(s) {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			lt = len(s) - i
		}
		if lt > 0 {
			top := stack[len(stack)-1]
			top.children = append(top.children, &node{text: unescape(s[i : i+lt])})
		}
		i += lt
		if i >= len(s) {
			break
		}
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i:], "-->")
			if end < 0 {
				return nil, errors.New("raster: unclosed comment")
			}
			i += end + 3
			continue
		case strings.HasPrefix(s[i:], "<?"), strings.HasPrefix(s[i:], "<!"):
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return nil, errors.New("raster: unclosed declaration")
			}
			i += end + 1
			continue
		}
		gt := strings.IndexByte(s[i:], '>')
		if gt < 0 {
			return nil, errors.New("raster: unclosed tag")
		}
		tag := s[i+1 : i+gt]
		i += gt + 1
		if strings.HasPrefix(tag, "/") {
			name := strings.TrimSpace(tag[1:])
			if len(stack) < 2 || stack[len(stack)-1].name != name {
				return nil, errors.New("raster: mismatched </" + name + ">")
			}
			stack = stack[:len(stack)-1]
			continue
		}
		self := strings.HasSuffix(tag, "/")
		if self {
			tag = tag[:len(tag)-1]
		}
		n, err := parseTag(tag)
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		top.children = append(top.children, n)
		if !self {
			if len(stack) > maxDepth {
				return nil, errors.New("raster: elements nest too deeply")
			}
			stack = append(stack, n)
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("raster: unclosed <" + stack[len(stack)-1].name + ">")
	}
	for _, c := range root.children {
		if c.name == "svg" {
			return c, nil
		}
	}
	return nil, errors.New("raster: no <svg> element")
}

func parseTag(tag string) (*node, error) {
	end := strings.IndexAny(tag, " \t\n\r")
	if end < 0 {
		end = len(tag)
	}
	n := &node{name: tag[:end]}
	if n.name == "" {
		return nil, errors.New("raster: empty tag")
	}
	rest := tag[end:]
	for {
		rest = strings.TrimLeft(rest, " \t\n\r")
		if rest == "" {
			return n, nil
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 0 || eq+1 >= len(rest) {
			return nil, errors.New("raster: malformed attribute in <" + n.name + ">")
		}
		name := strings.TrimSpace(rest[:eq])
		q := rest[eq+1]
		if q != '"' && q != '\'' {
			return nil, errors.New("raster: unquoted attribute in <" + n.name + ">")
		}
		close := strings.IndexByte(rest[eq+2:], q)
		if close < 0 {
			return nil, errors.New("raster: unclosed attribute in <" + n.name + ">")
		}
		n.attrs = append(n.attrs, [2]string{name, unescape(rest[eq+2 : eq+2+close])})
		rest = rest[eq+2+close+1:]
	}
}

func unescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 || semi > 12 {
			b.WriteByte('&')
			i++
			continue
		}
		ent := s[i+1 : i+semi]
		switch {
		case ent == "amp":
			b.WriteByte('&')
		case ent == "lt":
			b.WriteByte('<')
		case ent == "gt":
			b.WriteByte('>')
		case ent == "quot":
			b.WriteByte('"')
		case ent == "apos":
			b.WriteByte('\'')
		case strings.HasPrefix(ent, "#x") || strings.HasPrefix(ent, "#X"):
			v, err := strconv.ParseUint(ent[2:], 16, 32)
			if err != nil {
				b.WriteString(s[i : i+semi+1])
			} else {
				b.WriteRune(rune(v))
			}
		case strings.HasPrefix(ent, "#"):
			v, err := strconv.ParseUint(ent[1:], 10, 32)
			if err != nil {
				b.WriteString(s[i : i+semi+1])
			} else {
				b.WriteRune(rune(v))
			}
		default:
			b.WriteString(s[i : i+semi+1])
		}
		i += semi + 1
	}
	return b.String()
}
