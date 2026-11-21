package markdown

import (
	"fmt"
	"path"
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
	// Images reads an image that stands alone in its paragraph as an Image
	// block, for a reader that can draw it. Every other image is its words.
	Images bool
	// Dark says the reader's ground is dark: a <picture> shows the source for
	// prefers-color-scheme: dark, and otherwise the one for light.
	Dark bool
}

type htmlExprs struct {
	htmlComment, htmlDropped, htmlDetails, htmlLink, htmlIndent, htmlImage, htmlAlt, htmlSrc, htmlPicture, htmlSource, htmlSrcset, htmlMedia, htmlType, htmlHeading, htmlTag, refDefLine, noteDefLine, noteMark, refFull, alertLine, imageMark, imageLine *regexp.Regexp
}

var rx = sync.OnceValue(func() *htmlExprs {
	return &htmlExprs{
		htmlComment: regexp.MustCompile(`(?s)<!--.*?-->`),
		htmlDropped: regexp.MustCompile(`(?is)<(script|style|title|textarea|xmp|iframe|noembed|noframes|plaintext)\b[^>]*>.*?</(?:script|style|title|textarea|xmp|iframe|noembed|noframes|plaintext)\s*>`),
		htmlDetails: regexp.MustCompile(`(?is)<details\b([^>]*)>\s*(?:<summary\b[^>]*>(.*?)</summary>)?(.*?)</details\s*>`),
		htmlLink:    regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']*)["'][^>]*>(.*?)</a\s*>`),
		htmlIndent:  regexp.MustCompile(`(?m)^[ \t]+(</?[a-zA-Z])`),
		htmlImage:   regexp.MustCompile(`(?is)<img\b[^>]*>`),
		htmlAlt:     regexp.MustCompile(`(?is)\balt\s*=\s*["']([^"']*)["']`),
		htmlSrc:     regexp.MustCompile(`(?is)\bsrc\s*=\s*["']([^"'\s)<>]+)["']`),
		htmlPicture: regexp.MustCompile(`(?is)<picture\b[^>]*>(.*?)</picture\s*>`),
		htmlSource:  regexp.MustCompile(`(?is)<source\b[^>]*>`),
		htmlSrcset:  regexp.MustCompile(`(?is)\bsrcset\s*=\s*["']\s*([^"'\s,)<>]+)`),
		htmlMedia:   regexp.MustCompile(`(?is)\bmedia\s*=\s*["']([^"']*)["']`),
		htmlType:    regexp.MustCompile(`(?is)\btype\s*=\s*["']([^"']*)["']`),
		htmlHeading: regexp.MustCompile(`(?is)<h([1-6])\b[^>]*>(.*?)</h[1-6]\s*>`),
		htmlTag:     regexp.MustCompile(`(?s)</?[a-zA-Z][a-zA-Z0-9-]*(?:\s[^<>]*)?/?>`),
		refDefLine:  regexp.MustCompile(`(?m)^ {0,3}\[([^\]^][^\]]*)\]:[ ]+(\S+)[^\n]*$`),
		noteDefLine: regexp.MustCompile(`(?m)^ {0,3}\[\^([^\]]+)\]:[ ]+(.*)$`),
		noteMark:    regexp.MustCompile(`\[\^([^\]\s]+)\]`),
		refFull:     regexp.MustCompile(`\[([^\[\]]+)\]\[([^\[\]]*)\]`),
		alertLine:   regexp.MustCompile(`(?i)^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*`),
		imageMark:   regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`),
		imageLine:   regexp.MustCompile(`^!\[([^\]\n]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"\n]*")?\s*\)$`),
	}
})

var htmlInline = map[string]string{
	"b": "**", "strong": "**", "i": "*", "em": "*", "code": "`", "kbd": "`",
	"s": "~~", "del": "~~", "strike": "~~",
}

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
		parts[i] = plainImages(parts[i], opt.Images)
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

func cleanHTML(s string, opt Options) string {
	s = rx().htmlIndent.ReplaceAllString(s, "$1")
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
	s = rx().htmlPicture.ReplaceAllStringFunc(s, func(m string) string { return picture(m, opt.Dark) })
	s = rx().htmlLink.ReplaceAllStringFunc(s, func(m string) string {
		g := rx().htmlLink.FindStringSubmatch(m)
		return "[" + rx().htmlImage.ReplaceAllStringFunc(g[2], htmlImageWords) + "](" + g[1] + ")"
	})
	s = rx().htmlHeading.ReplaceAllStringFunc(s, func(m string) string {
		g := rx().htmlHeading.FindStringSubmatch(m)
		n, _ := strconv.Atoi(g[1])
		return "\n\n" + strings.Repeat("#", n) + " " + strings.TrimSpace(rx().htmlTag.ReplaceAllString(g[2], "")) + "\n\n"
	})
	s = rx().htmlImage.ReplaceAllStringFunc(s, func(m string) string {
		if g := rx().htmlSrc.FindStringSubmatch(m); opt.Images && g != nil && Drawable(g[1], "") {
			alt := "image"
			if a := rx().htmlAlt.FindStringSubmatch(m); a != nil && a[1] != "" {
				alt = a[1]
			}
			return "\n\n![" + strings.ReplaceAll(alt, "]", "") + "](" + g[1] + ")\n\n"
		}
		return htmlImageWords(m)
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

func alert(text string) (kind, rest string) {
	m := rx().alertLine.FindString(text)
	if m == "" {
		return "", text
	}
	return strings.ToUpper(rx().alertLine.FindStringSubmatch(text)[1]), text[len(m):]
}

func htmlImageWords(m string) string {
	alt := ""
	if g := rx().htmlAlt.FindStringSubmatch(m); g != nil {
		alt = g[1]
	}
	return imageWords(alt)
}

func imageWords(alt string) string {
	alt = strings.Join(strings.Fields(alt), " ")
	if alt == "" {
		alt = "image"
	}
	if r := []rune(alt); len(r) > imageWordsMax {
		cut := string(r[:imageWordsMax])
		if i := strings.LastIndexByte(cut, ' '); i > imageWordsMax/2 {
			cut = cut[:i]
		}
		alt = strings.TrimRight(cut, " ,.;:") + "…"
	}
	return "\\[Image: " + strings.NewReplacer("[", "\\[", "]", "\\]").Replace(alt) + "\\]"
}

const imageWordsMax = 40

func picture(m string, dark bool) string {
	alt := ""
	img := rx().htmlImage.FindString(m)
	if g := rx().htmlAlt.FindStringSubmatch(img); g != nil {
		alt = g[1]
	}
	alt = strings.NewReplacer(`"`, "", "<", "", ">", "").Replace(alt)
	for _, src := range rx().htmlSource.FindAllString(m, -1) {
		set := rx().htmlSrcset.FindStringSubmatch(src)
		if set == nil {
			continue
		}
		typ := ""
		if g := rx().htmlType.FindStringSubmatch(src); g != nil {
			typ = g[1]
		}
		if !Drawable(set[1], typ) {
			continue
		}
		if g := rx().htmlMedia.FindStringSubmatch(src); g != nil {
			media := strings.ToLower(g[1])
			if strings.Contains(media, "prefers-color-scheme") && strings.Contains(media, "dark") != dark {
				continue
			}
		}
		return `<img alt="` + alt + `" src="` + set[1] + `">`
	}
	if img == "" {
		return ""
	}
	return img
}

// Drawable reports whether an image at src, of MIME type typ when the text
// names one, is a kind a reader can draw: PNG, JPEG or GIF, on this machine.
// An address on the web, a WebP, an SVG and an AVIF are not.
func Drawable(src, typ string) bool {
	low := strings.ToLower(src)
	if strings.HasPrefix(low, "//") || strings.Contains(low, "://") && !strings.HasPrefix(low, "file://") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "":
	case "image/png", "image/jpeg", "image/jpg", "image/gif":
		return true
	default:
		return false
	}
	if i := strings.IndexAny(low, "?#"); i >= 0 {
		low = low[:i]
	}
	switch path.Ext(low) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return false
}

func plainImages(s string, keep bool) string {
	if !strings.Contains(s, "![") {
		return s
	}
	if keep {
		lines := strings.SplitAfter(s, "\n")
		for i, l := range lines {
			if body := strings.TrimSpace(l); rx().imageLine.MatchString(body) {
				continue
			}
			lines[i] = plainImages(l, false)
		}
		return strings.Join(lines, "")
	}
	return rx().imageMark.ReplaceAllStringFunc(s, func(m string) string {
		return imageWords(rx().imageMark.FindStringSubmatch(m)[1])
	})
}
