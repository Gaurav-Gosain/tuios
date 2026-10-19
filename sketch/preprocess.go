// Ported from pele src/preprocess.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// RE_DIRECTIVE uses a lookahead, which Go's regexp lacks: matchDirective is a
// hand-written matcher that takes the same text.

package sketch

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type Config = types.Config

type preprocessed struct {
	text string
	// title is nil for none.
	title  *string
	config Config
	// The text before its comment lines are removed, and the number of front matter lines before it.
	withComments string
	lineOffset   int
}

var (
	// /^\s*%%(?!{)[^\n]+\n?/gm
	reComment  = regexp.MustCompile(`(?m)^[` + js.S + `]*%%[^{\n][^\n]*\n?`)
	reHTMLTag  = regexp.MustCompile(`<(\w+)([^>]*)>`)
	reAttr     = regexp.MustCompile(`="([^"]*)"`)
	reCRLF     = regexp.MustCompile(`\r\n?`)
	unsafeKeys = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}
)

func cleanupComments(text string) string {
	if strings.Contains(text, "%%") {
		text = reComment.ReplaceAllString(text, "")
	}
	return js.TrimStart(text)
}

func isLineBreak(ch rune) bool { return ch == '\n' || ch == '\r' }

// runeAt is the character at byte i, or -1 past the end.
func runeAt(s string, i int) (rune, int) {
	if i < 0 || i >= len(s) {
		return -1, 1
	}
	return utf8.DecodeRuneInString(s[i:])
}

type frontMatter struct {
	indent string
	body   string
	length int
}

// lastIndexFrom is text.lastIndexOf(needle, from).
func lastIndexFrom(text, needle string, from int) int {
	end := from + len(needle)
	if end > len(text) {
		end = len(text)
	}
	if end < 0 {
		return -1
	}
	return strings.LastIndex(text[:end], needle)
}

// indexFrom is text.indexOf(needle, from).
func indexFrom(text, needle string, from int) int {
	if from < 0 {
		from = 0
	}
	if from > len(text) {
		return -1
	}
	i := strings.Index(text[from:], needle)
	if i < 0 {
		return -1
	}
	return from + i
}

// Finds a `---` fenced YAML block at the start of the text the way Mermaid's front matter regex does.
func matchFrontMatter(text string) (frontMatter, bool) {
	cursor := 0
	for cursor < len(text) {
		r, size := runeAt(text, cursor)
		if !js.IsSpace(r) || isLineBreak(r) {
			break
		}
		cursor += size
	}
	indent := text[:cursor]
	if !strings.HasPrefix(text[cursor:], "---") {
		return frontMatter{}, false
	}

	var openingEnds []int
	for i := cursor + 3; i < len(text); {
		r, size := runeAt(text, i)
		if !js.IsSpace(r) {
			break
		}
		if isLineBreak(r) {
			openingEnds = append(openingEnds, i)
		}
		i += size
	}
	if len(openingEnds) == 0 {
		return frontMatter{}, false
	}

	fence := indent + "---"
	closingEnd := func(i int) int {
		r, _ := runeAt(text, i)
		if !isLineBreak(r) || !strings.HasPrefix(text[i+1:], fence) {
			return -1
		}
		last := -1
		for j := i + 1 + len(fence); j < len(text); {
			r, size := runeAt(text, j)
			if !js.IsSpace(r) {
				break
			}
			if isLineBreak(r) {
				last = j
			}
			j += size
		}
		return last
	}

	needle := "\n" + fence
	lastFence := -1
	for i := strings.LastIndex(text, needle); i > openingEnds[0]; i = lastIndexFrom(text, needle, i-1) {
		if closingEnd(i) != -1 {
			lastFence = i
			break
		}
	}
	if lastFence == -1 {
		return frontMatter{}, false
	}

	openingEnd := openingEnds[0]
	for _, candidate := range openingEnds {
		if candidate < lastFence {
			openingEnd = candidate
		}
	}

	bodyStart := openingEnd + 1
	firstAt := 0
	if bodyStart > 0 {
		firstAt = bodyStart - 1
	}
	for i := indexFrom(text, needle, firstAt); i != -1 && i <= lastFence; i = indexFrom(text, needle, i+1) {
		if i < bodyStart {
			continue
		}
		end := closingEnd(i)
		if end != -1 {
			return frontMatter{indent: indent, body: text[bodyStart:i], length: end + 1}, true
		}
	}
	return frontMatter{}, false
}

func merge(target, source Config) Config {
	for key, value := range source {
		if unsafeKeys[key] {
			continue
		}
		current := target[key]
		vm, vok := value.(map[string]any)
		cm, cok := current.(map[string]any)
		if vok && cok {
			merge(cm, vm)
		} else if vok {
			target[key] = merge(Config{}, vm)
		} else {
			target[key] = value
		}
	}
	return target
}

type directive struct {
	start, end int
	// type is group 1 or 2; args is group 4.
	typ  string
	args string
}

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func skipSpace(s string, i int) int {
	for i < len(s) {
		r, size := runeAt(s, i)
		if !js.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// matchDirectiveAt matches
// /%{2}{\s*(?:(\w+)\s*:|(\w+))\s*(?:(\w+)|((?:(?!}%{2}).|\r?\n)*))?\s*(?:}%{2})?/ at i.
func matchDirectiveAt(s string, i int) (directive, bool) {
	if !strings.HasPrefix(s[i:], "%%{") {
		return directive{}, false
	}
	p := skipSpace(s, i+3)
	w := p
	for w < len(s) && isWordByte(s[w]) {
		w++
	}
	if w == p {
		return directive{}, false
	}
	d := directive{start: i, typ: s[p:w]}
	after := skipSpace(s, w)
	if after < len(s) && s[after] == ':' {
		p = after + 1
	} else {
		p = w
	}
	p = skipSpace(s, p)
	if p < len(s) && isWordByte(s[p]) {
		for p < len(s) && isWordByte(s[p]) {
			p++
		}
	} else {
		a := p
		for p < len(s) {
			if strings.HasPrefix(s[p:], "}%%") {
				break
			}
			if s[p] == '\r' && p+1 < len(s) && s[p+1] == '\n' {
				p += 2
				continue
			}
			r, size := runeAt(s, p)
			if r == '\r' || r == 0x2028 || r == 0x2029 {
				break
			}
			p += size
		}
		d.args = s[a:p]
	}
	p = skipSpace(s, p)
	if strings.HasPrefix(s[p:], "}%%") {
		p += 3
	}
	d.end = p
	return d, true
}

func findDirectives(s string) []directive {
	var out []directive
	for from := 0; from < len(s); {
		at := indexFrom(s, "%%{", from)
		if at == -1 {
			break
		}
		if d, ok := matchDirectiveAt(s, at); ok {
			out = append(out, d)
			from = d.end
		} else {
			from = at + 1
		}
	}
	return out
}

func directives(text string, config Config) string {
	if !strings.Contains(text, "%%{") {
		return text
	}
	normalized := strings.ReplaceAll(js.Trim(text), "'", "\"")
	for _, match := range findDirectives(normalized) {
		typ := match.typ
		if typ == "wrap" {
			config["wrap"] = true
		} else if (typ == "init" || typ == "initialize") && match.args != "" {
			var args any
			// Mermaid ignores an init directive whose body is not valid JSON.
			if json.Unmarshal([]byte(js.Trim(match.args)), &args) == nil {
				if m, ok := args.(map[string]any); ok {
					merge(config, m)
				}
			}
		}
	}
	var out strings.Builder
	last := 0
	for _, d := range findDirectives(text) {
		out.WriteString(text[last:d.start])
		last = d.end
	}
	out.WriteString(text[last:])
	return out.String()
}

func preprocess(source string) preprocessed {
	text := source
	if strings.Contains(text, "\r") {
		text = reCRLF.ReplaceAllString(text, "\n")
	}
	// A tag needs a closing bracket, so nothing after the last one can match.
	lastClose := -1
	if strings.Contains(text, "<") {
		lastClose = strings.LastIndexByte(text, '>')
	}
	if lastClose > 0 {
		head := reHTMLTag.ReplaceAllStringFunc(text[:lastClose+1], func(m string) string {
			sub := reHTMLTag.FindStringSubmatch(m)
			return "<" + sub[1] + reAttr.ReplaceAllString(sub[2], "='$1'") + ">"
		})
		text = head + text[lastClose+1:]
	}

	config := Config{}
	var title *string
	lineOffset := 0

	if strings.Contains(text, "---") {
		if front, ok := matchFrontMatter(text); ok {
			body := front.body
			if front.indent != "" {
				lines := strings.Split(front.body, "\n")
				for i, line := range lines {
					lines[i] = strings.TrimPrefix(line, front.indent)
				}
				body = strings.Join(lines, "\n")
			}
			parsed, err := util.ParseYaml(body)
			if err != nil {
				types.Throw("Front matter is not valid YAML. "+err.Error(), types.CodeSyntax, types.ErrorInfo{})
			}
			if p, ok := parsed.(map[string]any); ok {
				if js.Truthy(p["title"]) {
					t := js.String(p["title"])
					title = &t
				}
				if c, ok := p["config"].(map[string]any); ok {
					merge(config, c)
				}
				if js.Truthy(p["displayMode"]) {
					gantt, ok := config["gantt"].(map[string]any)
					if !ok {
						gantt = map[string]any{}
						config["gantt"] = gantt
					}
					gantt["displayMode"] = js.String(p["displayMode"])
				}
			}
			for i := strings.IndexByte(text, '\n'); i != -1 && i < front.length; i = indexFrom(text, "\n", i+1) {
				lineOffset++
			}
			text = text[front.length:]
		}
	}

	text = directives(text, config)
	return preprocessed{text: cleanupComments(text), title: title, config: config, withComments: text, lineOffset: lineOffset}
}

var (
	reEntity = regexp.MustCompile(`#\w+;`)
	reIntEnt = regexp.MustCompile(`^\+?\d+$`)
)

// lineEndFrom is the index of the first line terminator at or after from, or len(text).
func lineEndFrom(text string, from int) (int, int) {
	for i := from; i < len(text); {
		r, size := runeAt(text, i)
		if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 {
			return i, size
		}
		i += size
	}
	return len(text), 0
}

// Does what Mermaid's `text.replace(/keyword.*:\S*#.*;/g, drop the last character)` does:
// on a line where the keyword is followed by a colon, a color-like `#`, and later a semicolon,
// the last semicolon goes. Mermaid's regex backtracks without bound; this is one pass.
func dropColorSemicolons(text, keyword string) string {
	from := strings.Index(text, keyword)
	if from == -1 {
		return text
	}
	var out strings.Builder
	last := 0
	for from != -1 {
		lineEnd, _ := lineEndFrom(text, from)
		afterColon := false
		hash := -1
		for j := from + len(keyword); j < lineEnd; {
			c, size := runeAt(text, j)
			if c == 58 {
				afterColon = true
			} else if c == 35 {
				if afterColon {
					hash = j
					break
				}
			} else if util.IsSpace(c) {
				afterColon = false
			}
			j += size
		}
		if hash != -1 {
			semi := lastIndexFrom(text, ";", lineEnd-1)
			if semi > hash {
				out.WriteString(text[last:semi])
				last = semi + 1
			}
		}
		if lineEnd >= len(text) {
			from = -1
		} else {
			from = indexFrom(text, keyword, lineEnd+1)
		}
	}
	if last == 0 {
		return text
	}
	return out.String() + text[last:]
}

// Mermaid hides `#name;` entity codes behind placeholder characters so `#` and `;` survive parsing.
func encodeEntities(text string) string {
	if !strings.Contains(text, "#") {
		return text
	}
	return reEntity.ReplaceAllStringFunc(dropColorSemicolons(dropColorSemicolons(text, "style"), "classDef"), func(s string) string {
		inner := s[1 : len(s)-1]
		if reIntEnt.MatchString(inner) {
			return "ﬂ°°" + inner + "¶ß"
		}
		return "ﬂ°" + inner + "¶ß"
	})
}
