// Ported from pele src/diagrams/common/tokens.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// The port reads UTF-8 bytes, and offsets are byte offsets. pele's sticky regular
// expressions use lookaround, which Go's regexp lacks, so each pattern is a hand matcher
// (Match) here. A type with a literal pattern keeps it in Pattern.

package common

import (
	"strings"
	"sync"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// Tokenizer for the diagram types Mermaid defines with Langium grammars. Token types are tried
// in order and the first match wins, as in Chevrotain; the tables in each diagram's folder are
// checked against Mermaid's generated lexers by the tests.

type TokenType struct {
	Name string
	// A literal string. A type without one has Match or Scanner.
	Pattern string
	Hidden  bool
	// Start of a construct that needs a closer. When this matches but the whole pattern does not,
	// no closer exists in the rest of the text, so the pattern is not tried again.
	Opener Scanner
	// Every ASCII character a match of a regular expression can start with, when that is known.
	// The type is then tried only at those characters and at characters outside ASCII.
	First string
	// Chevrotain's LONGER_ALT. When this type matches, these are tried at the same place, and the
	// first one that matches more text becomes the token.
	Longer []*TokenType
	// Makes a matcher that answers as `pattern` does, for a pattern that could rescan the text.
	// It is called once per text, so the matcher may remember what it has scanned. The matcher
	// returns the end of the match, or -1.
	Scanner func() Scanner
	// Matches in place of the pattern, for a pattern that would rescan text from every position
	// and needs no memory between calls. Returns the end of the match, or -1.
	Match Scanner
}

type Scanner func(src string, at int) int

type Tokens struct {
	Kinds  []int
	Starts []int
	Ends   []int
	Src    string
}

type table struct {
	// For each ASCII character, the token types that can start with it, in matching order.
	starting [128][]int
	every    []int
	longer   [][]int
}

var tables sync.Map

func indexOf(types []*TokenType, t *TokenType) int {
	for k, other := range types {
		if other == t {
			return k
		}
	}
	return -1
}

func prepare(types []*TokenType) *table {
	t := &table{every: make([]int, len(types)), longer: make([][]int, len(types))}
	for k := range types {
		t.every[k] = k
	}
	for c := 0; c < 128; c++ {
		for k, typ := range types {
			if typ.Pattern != "" {
				if typ.Pattern[0] == byte(c) {
					t.starting[c] = append(t.starting[c], k)
				}
			} else if typ.First == "" || strings.IndexByte(typ.First, byte(c)) >= 0 {
				t.starting[c] = append(t.starting[c], k)
			}
		}
	}
	for k, typ := range types {
		if typ.Longer != nil {
			for _, alt := range typ.Longer {
				t.longer[k] = append(t.longer[k], indexOf(types, alt))
			}
		}
	}
	tables.Store(&types[0], t)
	return t
}

type tokenizer struct {
	src      string
	types    []*TokenType
	dead     []bool
	scanners []Scanner
}

func (z *tokenizer) match(k, p int) int {
	if k < 0 || z.dead[k] {
		return -1
	}
	typ := z.types[k]
	if typ.Match != nil {
		return typ.Match(z.src, p)
	}
	if typ.Scanner != nil {
		if z.scanners[k] == nil {
			z.scanners[k] = typ.Scanner()
		}
		return z.scanners[k](z.src, p)
	}
	if typ.Pattern != "" {
		if strings.HasPrefix(z.src[p:], typ.Pattern) {
			return p + len(typ.Pattern)
		}
		return -1
	}
	return -1
}

func Tokenize(src string, tokenTypes []*TokenType, diagram string) Tokens {
	var kinds, starts, ends []int
	var tb *table
	if len(tokenTypes) > 0 {
		if cached, ok := tables.Load(&tokenTypes[0]); ok {
			tb = cached.(*table)
		} else {
			tb = prepare(tokenTypes)
		}
	} else {
		tb = &table{}
	}
	z := &tokenizer{src: src, types: tokenTypes, dead: make([]bool, len(tokenTypes)), scanners: make([]Scanner, len(tokenTypes))}
	n := len(src)

	p := 0
scan:
	for p < n {
		c := src[p]
		candidates := tb.every
		if c < 128 {
			candidates = tb.starting[c]
		}
		for _, k := range candidates {
			end := z.match(k, p)
			if end <= p {
				if end < 0 && !z.dead[k] && tokenTypes[k].Opener != nil && tokenTypes[k].Opener(src, p) >= 0 {
					z.dead[k] = true
				}
				continue
			}
			kind := k
			for _, alt := range tb.longer[k] {
				altEnd := z.match(alt, p)
				if altEnd > end {
					kind = alt
					end = altEnd
					break
				}
			}
			if !tokenTypes[kind].Hidden {
				kinds = append(kinds, kind)
				starts = append(starts, p)
				ends = append(ends, end)
			}
			p = end
			continue scan
		}
		panic(types.SyntaxError(diagram, src, p, "", true))
	}
	kinds = append(kinds, -1)
	starts = append(starts, n)
	ends = append(ends, n)
	return Tokens{kinds, starts, ends, src}
}

// The character at a byte offset, as the patterns test it: -1 past the end.
func runeAt(src string, q int) rune {
	if q < 0 || q >= len(src) {
		return -1
	}
	if src[q] < utf8.RuneSelf {
		return rune(src[q])
	}
	r, _ := utf8.DecodeRuneInString(src[q:])
	return r
}

func byteAt(src string, q int) int {
	if q < 0 || q >= len(src) {
		return -1
	}
	return int(src[q])
}

// [\t ]*
func blanks(src string, q int) int {
	for q < len(src) && (src[q] == ' ' || src[q] == '\t') {
		q++
	}
	return q
}

// [^\n\r]*
func lineEnd(src string, q int) int {
	for q < len(src) && src[q] != '\n' && src[q] != '\r' {
		q++
	}
	return q
}

// [^\n\r]*?(?=%%)|[^\n\r]*
func toCommentOrLineEnd(src string, q int) int {
	end := lineEnd(src, q)
	if at := strings.Index(src[q:end], "%%"); at >= 0 {
		return q + at
	}
	return end
}

// (?:\r?\n|(?!\S))
func lineBreakOrSpace(src string, q int) int {
	if strings.HasPrefix(src[q:], "\r\n") {
		return q + 2
	}
	if strings.HasPrefix(src[q:], "\n") {
		return q + 1
	}
	if c := runeAt(src, q); c < 0 || js.IsSpace(c) {
		return q
	}
	return -1
}

// /[\t ]*accDescr(?:[\t ]*:([^\n\r]*?(?=%%)|[^\n\r]*)|\s*{([^}]*)})/y
func matchAccDescr(src string, p int) int {
	q := blanks(src, p)
	if !strings.HasPrefix(src[q:], "accDescr") {
		return -1
	}
	q += len("accDescr")
	if r := blanks(src, q); byteAt(src, r) == ':' {
		return toCommentOrLineEnd(src, r+1)
	}
	for q < len(src) {
		c, size := utf8.DecodeRuneInString(src[q:])
		if !js.IsSpace(c) {
			break
		}
		q += size
	}
	if byteAt(src, q) != '{' {
		return -1
	}
	if close := strings.IndexByte(src[q+1:], '}'); close >= 0 {
		return q + 1 + close + 1
	}
	return -1
}

// /[\t ]*accTitle[\t ]*:(?:[^\n\r]*?(?=%%)|[^\n\r]*)/y
func matchAccTitle(src string, p int) int {
	q := blanks(src, p)
	if !strings.HasPrefix(src[q:], "accTitle") {
		return -1
	}
	q = blanks(src, q+len("accTitle"))
	if byteAt(src, q) != ':' {
		return -1
	}
	return toCommentOrLineEnd(src, q+1)
}

// /[\t ]*title(?:[\t ][^\n\r]*?(?=%%)|[\t ][^\n\r]*|)/y
func matchTitle(src string, p int) int {
	q := blanks(src, p)
	if !strings.HasPrefix(src[q:], "title") {
		return -1
	}
	q += len("title")
	if c := byteAt(src, q); c == ' ' || c == '\t' {
		return toCommentOrLineEnd(src, q+1)
	}
	return q
}

// /"([^"\\]|\\.)*"|'([^'\\]|\\.)*'/y
func matchString(src string, p int) int {
	quote := src[p]
	if quote != '"' && quote != '\'' {
		return -1
	}
	for q := p + 1; q < len(src); {
		switch src[q] {
		case quote:
			return q + 1
		case '\\':
			c := runeAt(src, q+1)
			if c < 0 || c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029 {
				return -1
			}
			_, size := utf8.DecodeRuneInString(src[q+1:])
			q += 1 + size
		default:
			q++
		}
	}
	return -1
}

// /\r?\n/y
func matchNewline(src string, p int) int {
	if src[p] == '\n' {
		return p + 1
	}
	if strings.HasPrefix(src[p:], "\r\n") {
		return p + 2
	}
	return -1
}

// /[\t ]+/y
func matchWhitespace(src string, p int) int {
	if q := blanks(src, p); q > p {
		return q
	}
	return -1
}

// /---[\t ]*\r?\n/y
func openYaml(src string, p int) int {
	if !strings.HasPrefix(src[p:], "---") {
		return -1
	}
	q := blanks(src, p+3)
	if strings.HasPrefix(src[q:], "\r\n") {
		return q + 2
	}
	if strings.HasPrefix(src[q:], "\n") {
		return q + 1
	}
	return -1
}

// ---(?:\r?\n|(?!\S))
func closeYaml(src string, q int) int {
	if !strings.HasPrefix(src[q:], "---") {
		return -1
	}
	return lineBreakOrSpace(src, q+3)
}

// /---[\t ]*\r?\n(?:[\S\s]*?\r?\n)?---(?:\r?\n|(?!\S))/y
func matchYaml(src string, p int) int {
	q := openYaml(src, p)
	if q < 0 {
		return -1
	}
	// The optional group is tried first, with its lazy body growing to each line break in turn.
	for c := q; c < len(src); c++ {
		if src[c] != '\n' {
			continue
		}
		if end := closeYaml(src, c+1); end >= 0 {
			return end
		}
	}
	return closeYaml(src, q)
}

// /[\t ]*%%{/y
func openDirective(src string, p int) int {
	q := blanks(src, p)
	if strings.HasPrefix(src[q:], "%%{") {
		return q + 3
	}
	return -1
}

// /[\t ]*%%{[\S\s]*?}%%(?:\r?\n|(?!\S))/y
func matchDirective(src string, p int) int {
	q := openDirective(src, p)
	if q < 0 {
		return -1
	}
	for {
		at := strings.Index(src[q:], "}%%")
		if at < 0 {
			return -1
		}
		q += at
		if end := lineBreakOrSpace(src, q+3); end >= 0 {
			return end
		}
		q++
	}
}

// /[\t ]*%%[^\n\r]*/y
func matchComment(src string, p int) int {
	q := blanks(src, p)
	if !strings.HasPrefix(src[q:], "%%") {
		return -1
	}
	return lineEnd(src, q+2)
}

var ACC_DESCR = &TokenType{Name: "ACC_DESCR", Match: matchAccDescr, First: "\t a"}
var ACC_TITLE = &TokenType{Name: "ACC_TITLE", Match: matchAccTitle, First: "\t a"}
var TITLE = &TokenType{Name: "TITLE", Match: matchTitle, First: "\t t"}
var STRING = &TokenType{Name: "STRING", Match: matchString, First: `"'`}
var NEWLINE = &TokenType{Name: "NEWLINE", Match: matchNewline, First: "\r\n"}
var WHITESPACE = &TokenType{Name: "WHITESPACE", Match: matchWhitespace, Hidden: true, First: "\t "}
var YAML = &TokenType{Name: "YAML", Match: matchYaml, Hidden: true, Opener: openYaml, First: "-"}
var DIRECTIVE = &TokenType{Name: "DIRECTIVE", Match: matchDirective, Hidden: true, Opener: openDirective, First: "\t %"}
var SINGLE_LINE_COMMENT = &TokenType{Name: "SINGLE_LINE_COMMENT", Match: matchComment, First: "\t %", Hidden: true}

type keywordMatcher string

// word(?:(?=%%)|(?!\S))
func (word keywordMatcher) match(src string, p int) int {
	if !strings.HasPrefix(src[p:], string(word)) {
		return -1
	}
	q := p + len(word)
	if strings.HasPrefix(src[q:], "%%") {
		return q
	}
	if c := runeAt(src, q); c < 0 || js.IsSpace(c) {
		return q
	}
	return -1
}

// Keyword: Mermaid ends a keyword only at whitespace, a comment, or the end of input.
func Keyword(word string) *TokenType {
	return &TokenType{Name: word, Match: keywordMatcher(word).match, First: word[:1]}
}

// /title([\t ][^\n\r]*|)/ on a TITLE token: the text after the keyword.
func afterWord(text, word string) string {
	at := strings.Index(text, word)
	if at < 0 {
		return ""
	}
	return text[at+len(word):]
}

// [\t ]{2,} as one space, as the m-flagged replace does.
func collapseBlanks(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		j := blanks(text, i)
		if j-i >= 2 {
			out.WriteByte(' ')
			i = j
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}

func single(text string) string {
	return collapseBlanks(js.Trim(text))
}

func TitleValue(text string) string {
	rest := afterWord(text, "title")
	if c := byteAt(rest, 0); c != ' ' && c != '\t' {
		rest = ""
	}
	return single(lineEndOf(rest))
}

func lineEndOf(text string) string { return text[:lineEnd(text, 0)] }

func AccTitleValue(text string) string {
	rest := afterWord(text, "accTitle")
	q := blanks(rest, 0)
	if byteAt(rest, q) != ':' {
		return single("")
	}
	return single(lineEndOf(rest[q+1:]))
}

func isLineTerminator(c rune) bool { return c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029 }

// .replace(/^\s*/gm, ”): the whitespace at the start of each line, line breaks included.
func trimLineStarts(text string) string {
	var out strings.Builder
	start := true
	for i := 0; i < len(text); {
		c, size := utf8.DecodeRuneInString(text[i:])
		if start && js.IsSpace(c) {
			i += size
			continue
		}
		start = isLineTerminator(c)
		out.WriteString(text[i : i+size])
		i += size
	}
	return out.String()
}

// .replace(/\s+$/gm, ”): a run of whitespace up to the last line break inside it, or all of a
// run that ends the text.
func trimLineEnds(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		c, size := utf8.DecodeRuneInString(text[i:])
		if !js.IsSpace(c) {
			out.WriteString(text[i : i+size])
			i += size
			continue
		}
		j := i
		last := -1
		for j < len(text) {
			d, n := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(d) {
				break
			}
			if j > i && isLineTerminator(d) {
				last = j
			}
			j += n
		}
		if j == len(text) {
			break
		}
		if last >= 0 {
			out.WriteString(text[last:j])
		} else {
			out.WriteString(text[i:j])
		}
		i = j
	}
	return out.String()
}

// .replace(/[\n\r]{2,}/gm, '\n')
func collapseBreaks(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		j := i
		for j < len(text) && (text[j] == '\n' || text[j] == '\r') {
			j++
		}
		if j-i >= 2 {
			out.WriteByte('\n')
			i = j
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}

func AccDescrValue(text string) string {
	rest := afterWord(text, "accDescr")
	if q := blanks(rest, 0); byteAt(rest, q) == ':' {
		return single(lineEndOf(rest[q+1:]))
	}
	open := strings.IndexByte(rest, '{')
	close := strings.IndexByte(rest, '}')
	if open < 0 || close < open {
		return ""
	}
	return collapseBreaks(collapseBlanks(trimLineEnds(trimLineStarts(rest[open+1 : close]))))
}

func escapeOf(c byte) (string, bool) {
	switch c {
	case 'b':
		return "\b", true
	case 'f':
		return "\f", true
	case 'n':
		return "\n", true
	case 'r':
		return "\r", true
	case 't':
		return "\t", true
	case 'v':
		return "\v", true
	case '0':
		return "\x00", true
	}
	return "", false
}

func StringValue(text string) string {
	if len(text) < 2 {
		return ""
	}
	if !strings.Contains(text, "\\") {
		return text[1 : len(text)-1]
	}
	var out strings.Builder
	for i := 1; i < len(text)-1; i++ {
		c := text[i]
		if c == '\\' {
			i++
			if i >= len(text) {
				break
			}
			if e, ok := escapeOf(text[i]); ok {
				out.WriteString(e)
				continue
			}
			_, size := utf8.DecodeRuneInString(text[i:])
			out.WriteString(text[i : i+size])
			i += size - 1
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}

// Reader reads tokens for a hand-written parser, with Chevrotain-style messages on failure.
type Reader struct {
	I       int
	Kinds   []int
	Tokens  Tokens
	Types   []*TokenType
	Diagram string
}

func NewReader(tokens Tokens, types []*TokenType, diagram string) *Reader {
	return &Reader{Kinds: tokens.Kinds, Tokens: tokens, Types: types, Diagram: diagram}
}

func (r *Reader) Kind() int {
	if r.I >= len(r.Kinds) {
		return -1
	}
	return r.Kinds[r.I]
}

func (r *Reader) Text(index int) string {
	if index >= len(r.Kinds) {
		index = len(r.Kinds) - 1
	}
	return r.Tokens.Src[r.Tokens.Starts[index]:r.Tokens.Ends[index]]
}

func (r *Reader) Take() string {
	text := r.Text(r.I)
	r.I++
	return text
}

func (r *Reader) Accept(kind int) bool {
	if r.Kind() != kind {
		return false
	}
	r.I++
	return true
}

func (r *Reader) Expect(kind int) string {
	if r.Kind() != kind {
		r.Fail("token of type '" + r.Types[kind].Name + "'")
	}
	return r.Take()
}

func (r *Reader) Fail(expected string) {
	found := "end of input"
	if r.Kind() != -1 {
		found = "`" + r.Text(r.I) + "`"
	}
	at := r.I
	if at >= len(r.Kinds) {
		at = len(r.Kinds) - 1
	}
	panic(types.SyntaxError(r.Diagram, r.Tokens.Src, r.Tokens.Starts[at], "Expecting "+expected+" but found "+found+".", false))
}
