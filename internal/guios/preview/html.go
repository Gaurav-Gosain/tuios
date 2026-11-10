package preview

import (
	"strings"

	"golang.org/x/net/html"
)

// An HTML file shows as its page's text and pictures, laid out as Markdown
// and drawn by the viewer's Markdown view: headings, paragraphs, lists,
// links, tables, quotes, code and the pictures of its folder. No web engine
// runs it: scripts, styles, forms and frames are dropped, and nothing is
// fetched, so a page from anywhere is safe to look at.

// htmlNodesMax bounds the elements read of a page.
const htmlNodesMax = 200_000

// HTMLToMarkdown turns an HTML page into Markdown for the viewer.
func HTMLToMarkdown(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return src
	}
	w := &mdWriter{}
	if t := findTitle(doc); t != "" && !hasH1(doc) {
		w.put("# ", t, "\n\n")
	}
	w.block(doc, 0)
	return strings.TrimSpace(w.b.String()) + "\n"
}

type mdWriter struct {
	b     strings.Builder
	nodes int
}

func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return strings.TrimSpace(textOf(n))
	}
	if n.Type == html.ElementNode && n.Data == "body" {
		return ""
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := findTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func hasH1(n *html.Node) bool {
	if n.Type == html.ElementNode && n.Data == "h1" {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasH1(c) {
			return true
		}
	}
	return false
}

// dropped are the elements whose content does not show.
var dropped = map[string]bool{"script": true, "style": true, "head": true, "noscript": true, "template": true,
	"iframe": true, "object": true, "embed": true, "form": true, "button": true, "input": true, "select": true,
	"textarea": true, "svg": true, "canvas": true, "nav": false}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// block writes n's children as blocks, indent spaces in (inside a list).
func (w *mdWriter) block(n *html.Node, indent int) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if w.nodes++; w.nodes > htmlNodesMax {
			return
		}
		switch c.Type {
		case html.TextNode:
			if t := collapse(c.Data); strings.TrimSpace(t) != "" {
				w.para(strings.TrimSpace(t), indent)
			}
			continue
		case html.ElementNode, html.DocumentNode:
		default:
			continue
		}
		if dropped[c.Data] {
			continue
		}
		pad := strings.Repeat(" ", indent)
		switch c.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			w.put(pad, strings.Repeat("#", int(c.Data[1]-'0')), " ", strings.TrimSpace(w.inline(c)), "\n\n")
		case "p", "figcaption", "summary", "dt", "dd", "caption":
			w.para(w.inline(c), indent)
		case "br":
			w.b.WriteString("\n")
		case "hr":
			w.put(pad, "---\n\n")
		case "pre":
			w.put(pad, "```", codeLang(c), "\n", strings.TrimRight(textOf(c), "\n"), "\n", pad, "```\n\n")
		case "blockquote":
			sub := &mdWriter{nodes: w.nodes}
			sub.block(c, 0)
			w.nodes = sub.nodes
			for _, line := range strings.Split(strings.TrimSpace(sub.b.String()), "\n") {
				w.put(pad, "> ", line, "\n")
			}
			w.b.WriteString("\n")
		case "ul", "ol":
			k := 1
			for li := c.FirstChild; li != nil; li = li.NextSibling {
				if li.Type != html.ElementNode || li.Data != "li" {
					continue
				}
				mark := "- "
				if c.Data == "ol" {
					mark = itoa(k) + ". "
					k++
				}
				w.item(li, indent, mark)
			}
			w.b.WriteString("\n")
		case "table":
			w.table(c, pad)
		case "img":
			w.para(w.inline(c), indent)
		default:
			if inlineTag[c.Data] {
				w.para(w.inline(c), indent)
				continue
			}
			w.block(c, indent)
		}
	}
}

// item writes a list item: its own text on the marker's line, the blocks
// inside it under it.
func (w *mdWriter) item(li *html.Node, indent int, mark string) {
	pad := strings.Repeat(" ", indent)
	var text strings.Builder
	var rest []*html.Node
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "ul" || c.Data == "ol" || c.Data == "pre" || c.Data == "p" || c.Data == "table") {
			rest = append(rest, c)
			continue
		}
		text.WriteString(w.inlineNode(c))
	}
	w.put(pad, mark, strings.TrimSpace(collapse(text.String())), "\n")
	for _, c := range rest {
		holder := &html.Node{Type: html.ElementNode, Data: "div"}
		cp := *c
		cp.Parent, cp.PrevSibling, cp.NextSibling = nil, nil, nil
		holder.AppendChild(&cp)
		if c.Data == "p" {
			w.b.WriteString("\n")
		}
		w.block(holder, indent+len(mark))
	}
}

func (w *mdWriter) para(s string, indent int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	pad := strings.Repeat(" ", indent)
	w.put(pad, strings.ReplaceAll(s, "\n", "\n"+pad), "\n\n")
}

func (w *mdWriter) table(t *html.Node, pad string) {
	var rows [][]string
	head := -1
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == "tr" {
				var row []string
				th := false
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
						th = th || cell.Data == "th"
						row = append(row, strings.ReplaceAll(strings.TrimSpace(collapse(w.inline(cell))), "|", `\|`))
					}
				}
				if th && head < 0 {
					head = len(rows)
				}
				rows = append(rows, row)
				continue
			}
			walk(c)
		}
	}
	walk(t)
	if len(rows) == 0 {
		return
	}
	if head > 0 {
		rows[0], rows[head] = rows[head], rows[0]
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	line := func(r []string) {
		for len(r) < cols {
			r = append(r, "")
		}
		w.put(pad, "| ", strings.Join(r, " | "), " |\n")
	}
	line(rows[0])
	w.put(pad, "|", strings.Repeat(" --- |", cols), "\n")
	for _, r := range rows[1:] {
		line(r)
	}
	w.b.WriteString("\n")
}

var inlineTag = map[string]bool{"a": true, "b": true, "strong": true, "i": true, "em": true, "code": true, "span": true,
	"kbd": true, "sub": true, "sup": true, "s": true, "del": true, "mark": true, "small": true, "abbr": true, "time": true,
	"u": true, "ins": true, "label": true, "cite": true, "q": true, "img": true}

// inline is n's children as one line of Markdown.
func (w *mdWriter) inline(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "img" {
		return w.inlineNode(n)
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(w.inlineNode(c))
	}
	return collapse(b.String())
}

func (w *mdWriter) inlineNode(c *html.Node) string {
	if w.nodes++; w.nodes > htmlNodesMax {
		return ""
	}
	switch c.Type {
	case html.TextNode:
		return escapeMD(collapse(c.Data))
	case html.ElementNode:
	default:
		return ""
	}
	if dropped[c.Data] {
		return ""
	}
	in := w.inline(c)
	switch c.Data {
	case "b", "strong":
		return wrapTrim(in, "**")
	case "i", "em", "cite":
		return wrapTrim(in, "*")
	case "s", "del":
		return wrapTrim(in, "~~")
	case "code":
		return "`" + strings.ReplaceAll(textOf(c), "`", "'") + "`"
	case "a":
		href := attr(c, "href")
		if href == "" || strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return in
		}
		return "[" + in + "](" + href + ")"
	case "img":
		return "![" + attr(c, "alt") + "](" + attr(c, "src") + ")"
	case "br":
		return "<br>"
	case "kbd", "sub", "sup", "mark", "u", "ins":
		return "<" + c.Data + ">" + in + "</" + c.Data + ">"
	case "q":
		return "“" + in + "”"
	}
	return in
}

func wrapTrim(s, m string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return s
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " "))]
	trail := s[len(strings.TrimRight(s, " ")):]
	return lead + m + t + m + trail
}

func codeLang(pre *html.Node) string {
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "code" {
			for _, f := range strings.Fields(attr(c, "class")) {
				if l, ok := strings.CutPrefix(f, "language-"); ok {
					return l
				}
			}
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && dropped[n.Data] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// collapse folds runs of white space into one space, as a browser does.
func collapse(s string) string {
	if !strings.ContainsAny(s, "\n\t\r ") {
		return s
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	if len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t') {
		return " " + b.String()
	}
	return b.String()
}

// escapeMD keeps text from reading as Markdown syntax.
func escapeMD(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", "&lt;", "#", `\#`)
	return r.Replace(s)
}

// put writes each part in turn, with no string built of them first.
func (w *mdWriter) put(parts ...string) {
	for _, s := range parts {
		w.b.WriteString(s)
	}
}
