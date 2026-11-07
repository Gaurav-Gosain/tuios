package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Options says how a text is read.
type Options struct {
	// Max is the most bytes read, MaxBytes when it is 0.
	Max int
	// CloseDetails shows a <details> block as its summary alone. A text may ask for
	// that with its own open attribute.
	CloseDetails bool
}

// htmlExprs is the expressions of this file, compiled on first use: a client that
// never meets HTML does not pay for them at startup.
type htmlExprs struct {
	htmlComment, htmlDropped, htmlDetails, htmlLink, htmlImage, htmlAlt, htmlHeading, htmlTag, refDefLine, noteDefLine, noteMark, refFull, alertLine, imageMark *regexp.Regexp
}

var rx = sync.OnceValue(func() *htmlExprs {
	return &htmlExprs{
		htmlComment: regexp.MustCompile(`(?s)<!--.*?-->`),
		htmlDropped: regexp.MustCompile(`(?is)<(script|style|title|textarea|xmp|iframe|noembed|noframes|plaintext)\b[^>]*>.*?</(?:script|style|title|textarea|xmp|iframe|noembed|noframes|plaintext)\s*>`),
		htmlDetails: regexp.MustCompile(`(?is)<details\b([^>]*)>\s*(?:<summary\b[^>]*>(.*?)</summary>)?(.*?)</details\s*>`),
		htmlLink:    regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']*)["'][^>]*>(.*?)</a\s*>`),
		htmlImage:   regexp.MustCompile(`(?is)<img\b[^>]*>`),
		htmlAlt:     regexp.MustCompile(`(?is)\balt\s*=\s*["']([^"']*)["']`),
		htmlHeading: regexp.MustCompile(`(?is)<h([1-6])\b[^>]*>(.*?)</h[1-6]\s*>`),
		htmlTag:     regexp.MustCompile(`(?s)</?[a-zA-Z][a-zA-Z0-9-]*(?:\s[^<>]*)?/?>`),
		refDefLine:  regexp.MustCompile(`(?m)^ {0,3}\[([^\]^][^\]]*)\]:[ ]+(\S+)[^\n]*$`),
		noteDefLine: regexp.MustCompile(`(?m)^ {0,3}\[\^([^\]]+)\]:[ ]+(.*)$`),
		noteMark:    regexp.MustCompile(`\[\^([^\]\s]+)\]`),
		refFull:     regexp.MustCompile(`\[([^\[\]]+)\]\[([^\[\]]*)\]`),
		alertLine:   regexp.MustCompile(`(?i)^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*`),
		imageMark:   regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`),
	}
})

// htmlInline is the tags that have a Markdown form, as what opens and closes them.
var htmlInline = map[string]string{
	"b": "**", "strong": "**", "i": "*", "em": "*", "code": "`", "kbd": "`",
	"s": "~~", "del": "~~", "strike": "~~",
}

// clean turns the HTML, reference links and footnotes of a text into the Markdown
// the parser reads. It leaves code alone. A text with none of them costs a scan for
// two bytes.
func clean(src string, opt Options) string {
	if strings.IndexByte(src, '<') < 0 && !strings.Contains(src, "]") {
		return src
	}
	parts := splitFences(src)
	refs := map[string]string{}
	notes := map[string]string{}
	for i := 0; i < len(parts); i += 2 {
		parts[i] = rx().refDefLine.ReplaceAllStringFunc(parts[i], func(m string) string {
			g := rx().refDefLine.FindStringSubmatch(m)
			refs[strings.ToLower(g[1])] = strings.Trim(g[2], "<>")
			return ""
		})
		parts[i] = rx().noteDefLine.ReplaceAllStringFunc(parts[i], func(m string) string {
			g := rx().noteDefLine.FindStringSubmatch(m)
			notes[g[1]] = g[2]
			return ""
		})
	}
	var order []string
	seen := map[string]int{}
	for i := 0; i < len(parts); i += 2 {
		if strings.IndexByte(parts[i], '<') >= 0 {
			parts[i] = cleanHTML(parts[i], opt)
		}
		if len(refs) > 0 {
			parts[i] = rx().refFull.ReplaceAllStringFunc(parts[i], func(m string) string {
				g := rx().refFull.FindStringSubmatch(m)
				key := g[2]
				if key == "" {
					key = g[1]
				}
				if url, ok := refs[strings.ToLower(key)]; ok {
					return "[" + g[1] + "](" + url + ")"
				}
				return m
			})
		}
		if len(notes) > 0 {
			parts[i] = rx().noteMark.ReplaceAllStringFunc(parts[i], func(m string) string {
				id := rx().noteMark.FindStringSubmatch(m)[1]
				if _, ok := notes[id]; !ok {
					return m
				}
				if seen[id] == 0 {
					order = append(order, id)
					seen[id] = len(order)
				}
				return "\\[" + strconv.Itoa(seen[id]) + "\\]"
			})
		}
	}
	for i := 0; i < len(parts); i += 2 {
		parts[i] = plainImages(parts[i])
	}
	out := strings.Join(parts, "")
	if len(order) > 0 {
		var b strings.Builder
		b.WriteString(out)
		b.WriteString("\n\n---\n\n")
		for i, id := range order {
			fmt.Fprintf(&b, "%d. %s\n", i+1, notes[id])
		}
		out = b.String()
	}
	return out
}

// splitFences cuts a text at its fenced code: the even parts are text, and the odd
// parts are fences with their code.
func splitFences(src string) []string {
	var parts []string
	lines := strings.SplitAfter(src, "\n")
	start, at := 0, 0
	inside := false
	var ch byte
	var n int
	for _, l := range lines {
		line := strings.TrimRight(l, "\r\n")
		if !inside {
			if c, count, _, ok := fence(line); ok {
				parts = append(parts, src[start:at])
				start, inside, ch, n = at, true, c, count
			}
		} else if closes(line, ch, n) {
			parts = append(parts, src[start:at+len(l)])
			start, inside = at+len(l), false
		}
		at += len(l)
	}
	if inside {
		return append(parts, src[start:], "")
	}
	return append(parts, src[start:])
}

// cleanHTML turns HTML into Markdown. A tag with no Markdown form goes, and its text
// stays. Each pass is one regular expression over the text, so the cost is linear.
func cleanHTML(s string, opt Options) string {
	s = rx().htmlComment.ReplaceAllString(s, "")
	s = rx().htmlDropped.ReplaceAllString(s, "")
	s = rx().htmlDetails.ReplaceAllStringFunc(s, func(m string) string {
		g := rx().htmlDetails.FindStringSubmatch(m)
		head := "▸ " + strings.TrimSpace(g[2])
		if opt.CloseDetails && !strings.Contains(strings.ToLower(g[1]), "open") {
			return "\n\n" + head + "\n\n"
		}
		return "\n\n" + strings.Replace(head, "▸", "▾", 1) + "\n\n" + g[3] + "\n\n"
	})
	s = rx().htmlLink.ReplaceAllString(s, "[$2]($1)")
	s = rx().htmlHeading.ReplaceAllStringFunc(s, func(m string) string {
		g := rx().htmlHeading.FindStringSubmatch(m)
		n, _ := strconv.Atoi(g[1])
		return "\n\n" + strings.Repeat("#", n) + " " + strings.TrimSpace(rx().htmlTag.ReplaceAllString(g[2], "")) + "\n\n"
	})
	s = rx().htmlImage.ReplaceAllStringFunc(s, func(m string) string {
		alt := "image"
		if g := rx().htmlAlt.FindStringSubmatch(m); g != nil && g[1] != "" {
			alt = g[1]
		}
		return "\\[Image: " + alt + "\\]"
	})
	return rx().htmlTag.ReplaceAllStringFunc(s, func(m string) string {
		closing := strings.HasPrefix(m, "</")
		name := strings.TrimPrefix(strings.TrimPrefix(m, "</"), "<")
		if i := strings.IndexAny(name, " \t\n/>"); i >= 0 {
			name = name[:i]
		}
		name = strings.ToLower(name)
		if md, ok := htmlInline[name]; ok {
			return md
		}
		switch name {
		case "br":
			return "  \n"
		case "hr":
			return "\n\n---\n\n"
		case "li":
			if !closing {
				return "\n- "
			}
			return ""
		case "p", "div", "ul", "ol", "blockquote", "table", "tr", "pre", "center", "section", "picture":
			return "\n\n"
		case "td", "th":
			if !closing {
				return " "
			}
		}
		return ""
	})
}

// alert reads the first line of a quote's text: GitHub's [!NOTE] and the other four.
func alert(text string) (kind, rest string) {
	m := rx().alertLine.FindString(text)
	if m == "" {
		return "", text
	}
	return strings.ToUpper(rx().alertLine.FindStringSubmatch(text)[1]), text[len(m):]
}

// plainImages shows a Markdown image as its words.
func plainImages(s string) string {
	if !strings.Contains(s, "![") {
		return s
	}
	return rx().imageMark.ReplaceAllStringFunc(s, func(m string) string {
		alt := rx().imageMark.FindStringSubmatch(m)[1]
		if alt == "" {
			alt = "image"
		}
		return "\\[Image: " + alt + "\\]"
	})
}
