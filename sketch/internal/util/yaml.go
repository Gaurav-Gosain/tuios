// Ported from pele src/util/yaml.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele throws; the port panics with yamlError or incomplete and ParseYaml
// recovers them into an error.

package util

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
)

// A small YAML reader covering what Mermaid feeds to js-yaml with the JSON schema:
// block and flow collections, quoted and plain scalars, and block scalars.

// YamlValue is nil, bool, float64, string, []any or map[string]any.
type YamlValue = any

type yamlError struct{ message string }

// Thrown when a quoted scalar or flow collection runs past the text read so far.
type incomplete struct{ message string }

func fail(message string) { panic(yamlError{message}) }

const MAX_DEPTH = 64

type reader struct {
	lines []string
	i     int
}

var reCRLFYaml = lazyre.New(`\r\n?`)

func newReader(src string) *reader {
	if strings.Contains(src, "\r") {
		src = reCRLFYaml.ReplaceAllString(src, "\n")
	}
	return &reader{lines: strings.Split(src, "\n")}
}

func (r *reader) skipBlank() {
	for r.i < len(r.lines) {
		t := js.Trim(r.lines[r.i])
		if t != "" && t[0] != '#' && t != "---" {
			break
		}
		r.i++
	}
}

func (r *reader) indent() int {
	line := r.lines[r.i]
	k := 0
	for k < len(line) && line[k] == ' ' {
		k++
	}
	if k < len(line) && line[k] == '\t' {
		fail("YAML: tabs are not allowed for indentation")
	}
	return k
}

var (
	reInt         = lazyre.New(`^[-+]?(?:0|[1-9][0-9]*)$`)
	reHex         = lazyre.New(`^[-+]?0x[0-9a-fA-F]+$`)
	reOct         = lazyre.New(`^[-+]?0o[0-7]+$`)
	reBin         = lazyre.New(`^[-+]?0b[01]+$`)
	reFloat       = lazyre.New(`^[-+]?(?:[0-9][0-9]*(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?$`)
	reKeyStart    = lazyre.New(`[` + js.S + `#:,\[\]{}&*!|>'"%@` + "`" + `]`)
	reInf         = lazyre.New(`^[-+]?\.(?:inf|Inf|INF)$`)
	reNaN         = lazyre.New(`^\.(?:nan|NaN|NAN)$`)
	reFlowEnd     = lazyre.New(`[` + js.S + `,\[\]{}]`)
	reDigit19     = lazyre.New(`[1-9]`)
	reBlockHeader = lazyre.New(`^[|>][-+0-9]*[` + js.S + `]*(?:#` + js.Dot + `*)?$`)
	reLeadBlank   = lazyre.New(`^[ \t]`)
	reEscNL       = lazyre.New(`\\\n[ \t]*`)
	reNewlines    = lazyre.New(`\n+`)
)

// at is the byte of s at i, or 0 past its end, where pele reads undefined.
func at(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func isBlankCh(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

type keyMatch struct {
	key    string
	length int
}

// Splits `key: rest` at the start of a line. Returns the raw key and the offset where the value starts.
func splitKey(line string) (keyMatch, bool) {
	if line == "" {
		return keyMatch{}, false
	}
	first := line[0]
	var end, colon int
	if first == '"' || first == '\'' {
		i := 1
		for {
			if i >= len(line) {
				return keyMatch{}, false
			}
			c := line[i]
			if first == '"' && c == '\\' {
				i += 2
			} else if c == first {
				if first == '"' || at(line, i+1) != '\'' {
					break
				}
				i += 2
			} else {
				i++
			}
		}
		end = i + 1
		colon = end
		for isBlankCh(at(line, colon)) {
			colon++
		}
		if at(line, colon) != ':' {
			return keyMatch{}, false
		}
	} else {
		r, _ := utf8.DecodeRuneInString(line)
		if reKeyStart.MatchString(string(r)) {
			return keyMatch{}, false
		}
		if first == '-' && (len(line) < 2 || isBlankCh(line[1]) || line[1] == ':' || line[1] == '#') {
			return keyMatch{}, false
		}
		colon = 1
		for colon < len(line) && line[colon] != ':' && line[colon] != '#' {
			colon++
		}
		if at(line, colon) != ':' {
			return keyMatch{}, false
		}
		end = colon
		for end > 0 && isBlankCh(at(line, end-1)) {
			end--
		}
	}
	length := colon + 1
	if length < len(line) && !isBlankCh(line[length]) {
		return keyMatch{}, false
	}
	for isBlankCh(at(line, length)) {
		length++
	}
	return keyMatch{key: line[:end], length: length}, true
}

func scalar(raw string) YamlValue {
	s := js.Trim(raw)
	switch s {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	c := s[0]
	if c != '-' && c != '+' && c != '.' && (c < '0' || c > '9') {
		return s
	}
	if reInt.MatchString(s) || reFloat.MatchString(s) {
		return js.Number(s)
	}
	if reHex.MatchString(s) || reOct.MatchString(s) || reBin.MatchString(s) {
		neg := s[0] == '-'
		body := s
		if neg || s[0] == '+' {
			body = s[1:]
		}
		v := js.Number(body)
		if neg {
			return -v
		}
		return v
	}
	if reInf.MatchString(s) {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if reNaN.MatchString(s) {
		return math.NaN()
	}
	return s
}

var escapes = map[byte]string{
	'0':  "\x00",
	'a':  "\x07",
	'b':  "\b",
	't':  "\t",
	'n':  "\n",
	'v':  "\v",
	'f':  "\f",
	'r':  "\r",
	'e':  "\x1b",
	' ':  " ",
	'"':  "\"",
	'/':  "/",
	'\\': "\\",
	'N':  "\u0085",
	'_':  " ",
	'L':  " ",
	'P':  " ",
}

// Folds the line breaks of a scalar: blanks around a break go, one break becomes a space, and
// each further break stays. The lines are trimmed by hand, because a pattern for blanks before
// a line break rescans a long run of blanks from every one of them.
func fold(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	lines := strings.Split(text, "\n")
	last := len(lines) - 1
	for i := 0; i <= last; i++ {
		line := lines[i]
		from := 0
		to := len(line)
		if i > 0 {
			for from < to && (line[from] == 32 || line[from] == 9) {
				from++
			}
		}
		if i < last {
			for to > from && (line[to-1] == 32 || line[to-1] == 9) {
				to--
			}
		}
		lines[i] = line[from:to]
	}
	return reNewlines.ReplaceAllStringFunc(strings.Join(lines, "\n"), func(m string) string {
		if len(m) == 1 {
			return " "
		}
		return strings.Repeat("\n", len(m)-1)
	})
}

func unquoteDouble(body string) string {
	if !strings.Contains(body, `\`) {
		return fold(body)
	}
	var out strings.Builder

	s := fold(reEscNL.ReplaceAllString(body, ""))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '\\' {
			out.WriteByte(ch)
			continue
		}
		i++
		e := at(s, i)
		if e == 'x' || e == 'u' || e == 'U' {
			n := 8
			if e == 'x' {
				n = 2
			} else if e == 'u' {
				n = 4
			}
			end := i + 1 + n
			if end > len(s) {
				end = len(s)
			}
			cp := js.ParseIntPrefix(s[i+1:end], 16)
			if math.IsNaN(cp) || cp < 0 || cp > 0x10ffff {
				fail("Invalid code point " + js.Num(cp))
			}
			out.WriteRune(rune(cp))
			i += n
		} else if v, ok := escapes[e]; ok && i < len(s) {
			out.WriteString(v)
		} else {
			fail("YAML: unknown escape sequence")
		}
	}
	return out.String()
}

// prevIsSpace is `/\s/.test(s[pos - 1])`.
func prevIsSpace(s string, pos int) bool {
	if pos <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	return js.IsSpace(r)
}

func skip(s string, pos int, commas bool) int {
	for pos < len(s) {
		c := s[pos]
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' || (commas && c == ',') {
			pos++
		} else if c == '#' && (pos == 0 || prevIsSpace(s, pos)) {
			nl := strings.IndexByte(s[pos:], '\n')
			if nl == -1 {
				pos = len(s)
			} else {
				pos += nl
			}
		} else {
			break
		}
	}
	return pos
}

// Entries of a flow collection are separated by exactly one comma, as js-yaml requires.
func entryStart(ch byte, separated bool) {
	if !separated {
		fail("YAML: missed comma between flow collection entries")
	}
	if ch == ',' {
		fail("YAML: expected the node content, but found ','")
	}
}

// Parses a flow value starting at s[pos]; returns the value and the index after it.
func flow(s string, pos int, stops string, depth int) (YamlValue, int) {
	if depth > MAX_DEPTH {
		fail("YAML: nesting is too deep")
	}
	pos = skip(s, pos, false)
	ch := at(s, pos)
	if pos < len(s) && ch == '{' {
		out := map[string]any{}
		pos++
		separated := true
		for {
			pos = skip(s, pos, false)
			if pos >= len(s) {
				panic(incomplete{"YAML: unexpected end of the stream within a flow collection"})
			}
			if s[pos] == '}' {
				return out, pos + 1
			}
			entryStart(s[pos], separated)
			var key YamlValue
			key, pos = flow(s, pos, ":,}", depth+1)
			pos = skip(s, pos, false)
			var value YamlValue
			if at(s, pos) == ':' {
				value, pos = flow(s, pos+1, ",}", depth+1)
			}
			out[js.String(key)] = value
			pos = skip(s, pos, false)
			separated = at(s, pos) == ','
			if separated {
				pos++
			}
		}
	}
	if pos < len(s) && ch == '[' {
		out := []any{}
		pos++
		separated := true
		for {
			pos = skip(s, pos, false)
			if pos >= len(s) {
				panic(incomplete{"YAML: unexpected end of the stream within a flow collection"})
			}
			if s[pos] == ']' {
				return out, pos + 1
			}
			entryStart(s[pos], separated)
			var value YamlValue
			value, pos = flow(s, pos, ",]", depth+1)
			out = append(out, value)
			pos = skip(s, pos, false)
			separated = at(s, pos) == ','
			if separated {
				pos++
			}
		}
	}
	if pos < len(s) && ch == '"' {
		end := pos + 1
		for end < len(s) && s[end] != '"' {
			if s[end] == '\\' {
				end += 2
			} else {
				end++
			}
		}
		if end >= len(s) {
			panic(incomplete{"YAML: unexpected end of the stream within a double quoted scalar"})
		}
		return unquoteDouble(s[pos+1 : end]), end + 1
	}
	if pos < len(s) && ch == '\'' {
		end := pos + 1
		for end < len(s) {
			if s[end] == '\'' {
				if at(s, end+1) != '\'' {
					break
				}
				end++
			}
			end++
		}
		if end >= len(s) {
			panic(incomplete{"YAML: unexpected end of the stream within a single quoted scalar"})
		}
		return strings.ReplaceAll(fold(s[pos+1:end]), "''", "'"), end + 1
	}
	end := pos
	for end < len(s) {
		c := s[end]
		if strings.IndexByte(stops, c) >= 0 && (c != ':' || end+1 >= len(s) || nextIsFlowEnd(s, end+1)) {
			break
		}
		if c == '#' && end > pos && prevIsSpace(s, end) {
			nl := strings.IndexByte(s[end:], '\n')
			next := len(s)
			if nl != -1 {
				next = end + nl
			}
			return scalar(fold(js.Trim(s[pos:end]))), next
		}
		end++
	}
	return scalar(fold(js.Trim(s[pos:end]))), end
}

func nextIsFlowEnd(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return reFlowEnd.MatchString(string(r))
}

func stripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		} else if c == '"' || c == '\'' {
			quote = c
		} else if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

// leadingWidth is `line.length - line.trimStart().length`: the leading whitespace in UTF-16
// units, which count a no-break space as one where UTF-8 has two bytes.
func leadingWidth(line string) int { return js.Len(line[:len(line)-len(js.TrimStart(line))]) }

// sliceUnits is line.slice(k) for a k that falls within the leading whitespace, which is in the BMP:
// it skips k characters.
func sliceUnits(line string, k int) string {
	i := 0
	for ; k > 0 && i < len(line); k-- {
		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
	}
	return line[i:]
}

func blockScalar(r *reader, header string, parentIndent int) string {
	folded := header[0] == '>'
	chomp := ""
	if strings.Contains(header, "-") {
		chomp = "-"
	} else if strings.Contains(header, "+") {
		chomp = "+"
	}
	indent := -1
	if m := reDigit19.FindString(header); m != "" {
		indent = parentIndent + int(m[0]-'0')
	}
	var out []string
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		if js.Trim(line) == "" {
			out = append(out, "")
			r.i++
			continue
		}
		k := leadingWidth(line)
		if indent == -1 {
			if k <= parentIndent {
				break
			}
			indent = k
		}
		if k < indent {
			break
		}
		out = append(out, sliceUnits(line, indent))
		r.i++
	}
	trailing := 0
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
		trailing++
	}
	var text string
	if folded {
		var b strings.Builder
		for i, cur := range out {
			if i > 0 {
				prev := out[i-1]
				if cur == "" {
					b.WriteString("\n")
				} else if prev != "" {
					if reLeadBlank.MatchString(prev) || reLeadBlank.MatchString(cur) {
						b.WriteString("\n")
					} else {
						b.WriteString(" ")
					}
				}
			}
			b.WriteString(cur)
		}
		text = b.String()
	} else {
		text = strings.Join(out, "\n")
	}
	if len(out) == 0 {
		if chomp == "+" {
			return strings.Repeat("\n", trailing)
		}
		return ""
	}
	if chomp == "-" {
		return text
	}
	if chomp == "+" {
		return text + strings.Repeat("\n", trailing+1)
	}
	return text + "\n"
}

func inlineValue(r *reader, rest string, indent int) YamlValue {
	first := at(rest, 0)
	if first == '|' || first == '>' {
		if reBlockHeader.MatchString(rest) {
			return blockScalar(r, rest, indent)
		}
	}
	if first == '{' || first == '[' || first == '"' || first == '\'' {
		// The value may continue on later lines. Read them in growing batches until it closes.
		start := r.i
		text := rest
		taken := 0
		batch := 1
		for {
			value, end, inc := tryFlow(text)
			if inc == nil {
				used := 0
				for k := strings.IndexByte(text, '\n'); k != -1 && k < end; {
					used++
					j := strings.IndexByte(text[k+1:], '\n')
					if j == -1 {
						break
					}
					k = k + 1 + j
				}
				lineEnd := -1
				if end <= len(text) {
					if j := strings.IndexByte(text[end:], '\n'); j != -1 {
						lineEnd = end + j
					}
				}
				tail := text[end:]
				if lineEnd != -1 {
					tail = text[end:lineEnd]
				}
				if js.Trim(stripComment(tail)) != "" {
					fail("YAML: unexpected content after a value")
				}
				r.i = start + used
				return value
			}
			if start+taken >= len(r.lines) {
				fail(inc.message)
			}
			stop := start + taken + batch
			if stop > len(r.lines) {
				stop = len(r.lines)
			}
			more := r.lines[start+taken : stop]
			text += "\n" + strings.Join(more, "\n")
			taken += len(more)
			batch *= 2
		}
	}
	text := js.Trim(stripComment(rest))
	for r.i < len(r.lines) {
		line := r.lines[r.i]
		if js.Trim(line) == "" {
			j := r.i
			for j < len(r.lines) && js.Trim(r.lines[j]) == "" {
				j++
			}
			if j >= len(r.lines) || leadingWidth(r.lines[j]) <= indent {
				break
			}
			text += strings.Repeat("\n", j-r.i)
			r.i = j
			continue
		}
		if leadingWidth(line) <= indent {
			break
		}
		more := js.Trim(stripComment(line))
		if strings.HasSuffix(text, "\n") {
			text += more
		} else {
			text += " " + more
		}
		r.i++
	}
	return scalar(text)
}

// tryFlow runs flow and catches an incomplete value, as the batch loop does.
func tryFlow(text string) (value YamlValue, end int, inc *incomplete) {
	defer func() {
		if e := recover(); e != nil {
			if i, ok := e.(incomplete); ok {
				inc = &i
				return
			}
			panic(e)
		}
	}()
	value, end = flow(text, 0, "", 0)
	return value, end, nil
}

func isSeqLine(line string) bool { return line == "-" || strings.HasPrefix(line, "- ") }

func block(r *reader, minIndent int, depth int) YamlValue {
	if depth > MAX_DEPTH {
		fail("YAML: nesting is too deep")
	}
	r.skipBlank()
	if r.i >= len(r.lines) {
		return nil
	}
	indent := r.indent()
	if indent < minIndent {
		return nil
	}
	line := r.lines[r.i][indent:]

	if isSeqLine(line) {
		out := []any{}
		for r.i < len(r.lines) {
			r.skipBlank()
			if r.i >= len(r.lines) || r.indent() != indent {
				break
			}
			cur := r.lines[r.i][indent:]
			if !isSeqLine(cur) {
				break
			}
			rest := cur[1:]
			pad := leadingWidth(rest) + 1
			body := js.Trim(stripComment(rest))
			if body == "" {
				r.i++
				out = append(out, block(r, indent+1, depth+1))
			} else if _, ok := splitKey(body); ok {
				r.lines[r.i] = strings.Repeat(" ", indent+pad) + js.TrimStart(rest)
				out = append(out, block(r, indent+pad, depth+1))
			} else {
				r.i++
				out = append(out, inlineValue(r, js.Trim(rest), indent))
			}
		}
		return out
	}

	if _, ok := splitKey(line); !ok {
		r.i++
		return inlineValue(r, js.Trim(line), indent-1)
	}

	out := map[string]any{}
	for r.i < len(r.lines) {
		r.skipBlank()
		if r.i >= len(r.lines) {
			break
		}
		k := r.indent()
		if k < indent {
			break
		}
		if k > indent {
			fail("YAML: bad indentation of a mapping entry")
		}
		cur := r.lines[r.i][indent:]
		km, ok := splitKey(cur)
		if !ok {
			fail("YAML: can not read a block mapping entry")
		}
		kv, _ := flow(km.key, 0, "", 0)
		key := js.String(kv)
		rest := cur[km.length:]
		r.i++
		if js.Trim(stripComment(rest)) == "" {
			r.skipBlank()
			if r.i < len(r.lines) {
				next := r.indent()
				nextLine := r.lines[r.i][next:]
				seq := isSeqLine(nextLine)
				if next > indent || (seq && next == indent) {
					min := indent + 1
					if seq {
						min = indent
					}
					out[key] = block(r, min, depth+1)
				} else {
					out[key] = nil
				}
			} else {
				out[key] = nil
			}
		} else {
			out[key] = inlineValue(r, js.Trim(rest), indent)
		}
	}
	return out
}

// ParseYaml reads the YAML subset. A value it cannot read is an error.
func ParseYaml(src string) (value YamlValue, err error) {
	defer func() {
		if e := recover(); e != nil {
			switch t := e.(type) {
			case yamlError:
				err = errors.New(t.message)
			case incomplete:
				err = errors.New(t.message)
			default:
				panic(e)
			}
		}
	}()
	trimmed := js.Trim(src)
	if trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') {
		v, end := flow(trimmed, 0, "", 0)
		if skip(trimmed, end, false) == len(trimmed) {
			return v, nil
		}
	}
	r := newReader(src)
	value = block(r, 0, 0)
	r.skipBlank()
	if r.i < len(r.lines) && js.Trim(r.lines[r.i]) != "..." {
		fail("YAML: end of the stream or a document separator is expected")
	}
	return value, nil
}
