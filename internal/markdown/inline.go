package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Style is the set of inline styles a run carries.
type Style uint8

const (
	// Strong is **text** or __text__.
	Strong Style = 1 << iota
	// Emphasis is *text* or _text_.
	Emphasis
	// Strike is ~~text~~.
	Strike
	// CodeSpan is `text`.
	CodeSpan
	// Link is the text of a link, an autolink or a bare URL. The run's URL
	// is its target.
	Link
)

// Run is a stretch of text with one style. Text holds "\n" only for a hard
// line break.
type Run struct {
	Text  string
	Style Style
	URL   string
}

// Inline reads the inline source of a paragraph, heading or table cell into
// runs. A soft line break becomes a space.
func Inline(src string) []Run {
	p := inline{}
	p.parse(src, 0, "", 0)
	p.flush(0, "")
	return p.out
}

// inline is the state of one Inline call.
type inline struct {
	out []Run
	buf strings.Builder
	// linkWork is how many bytes the link searches have read. Past
	// linkWorkMax no more links are looked for, so a text of many open
	// brackets costs a bounded amount.
	linkWork int
}

// linkWorkMax bounds the bytes the link searches of one Inline call read.
const linkWorkMax = 256 << 10

// flush ends the plain text gathered so far as a run of st.
func (p *inline) flush(st Style, url string) {
	if p.buf.Len() == 0 {
		return
	}
	t := p.buf.String()
	p.buf.Reset()
	if n := len(p.out); n > 0 && p.out[n-1].Style == st && p.out[n-1].URL == url && p.out[n-1].Text != "\n" && t != "\n" {
		p.out[n-1].Text += t
		return
	}
	p.out = append(p.out, Run{Text: t, Style: st, URL: url})
}

// misses remembers the closer searches that failed in one parse call. A
// search that found nothing from one offset finds nothing from a later one,
// so a text of many unmatched openers is read once, not once per opener.
type misses struct {
	delim [3][4]int // by char (* _ ~) and run length: the offset searched from
	tick  [16]int   // by backtick run length
}

// parse reads s as text of style st and link url. depth is the nesting of
// styles.
func (p *inline) parse(s string, st Style, url string, depth int) {
	var miss misses
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '\n':
			p.flush(st, url)
			p.buf.WriteByte('\n')
			p.flush(st, url)
			i += 2
			continue
		case c == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			p.buf.WriteByte(s[i+1])
			i += 2
			continue
		case c == '\n':
			// Two spaces before a line break make it a hard break.
			if strings.HasSuffix(p.buf.String(), "  ") {
				t := strings.TrimRight(p.buf.String(), " ")
				p.buf.Reset()
				p.buf.WriteString(t)
				p.flush(st, url)
				p.buf.WriteByte('\n')
				p.flush(st, url)
			} else {
				p.buf.WriteByte(' ')
			}
			i++
			for i < len(s) && s[i] == ' ' {
				i++
			}
			continue
		case c == '`':
			n := run(s, i, '`')
			if n < len(miss.tick) && (miss.tick[n] == 0 || i < miss.tick[n]) {
				if end := closeTicks(s, i+n, n); end >= 0 {
					code := strings.ReplaceAll(s[i+n:end], "\n", " ")
					if len(code) >= 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
						code = code[1 : len(code)-1]
					}
					p.flush(st, url)
					p.buf.WriteString(code)
					p.flush(st|CodeSpan, url)
					i = end + n
					continue
				}
				miss.tick[n] = i + 1
			}
			p.buf.WriteString(s[i : i+n])
			i += n
			continue
		case c == '*' || c == '_' || c == '~':
			n := run(s, i, c)
			if end, add, ok := p.emphasis(s, i, n, depth, &miss); ok {
				p.flush(st, url)
				k := n
				if c != '~' && n > 2 {
					k = 3
				}
				p.parse(s[i+k:end], st|add, url, depth+1)
				p.flush(st|add, url)
				i = end + k
				continue
			}
			p.buf.WriteString(s[i : i+n])
			i += n
			continue
		case c == '[' || c == '!' && i+1 < len(s) && s[i+1] == '[':
			open := i
			if c == '!' {
				open++
			}
			if url == "" && depth < maxDepth && p.linkWork < linkWorkMax {
				end, dest, next, read, ok := link(s, open)
				p.linkWork += read
				if ok {
					p.flush(st, url)
					text := s[open+1 : end]
					if text == "" {
						text = dest
					}
					p.parse(text, st|Link, dest, depth+1)
					p.flush(st|Link, dest)
					i = next
					continue
				}
			}
			p.buf.WriteString(s[i : open+1])
			i = open + 1
			continue
		case c == '<' && url == "":
			if end := strings.IndexByte(s[i:min(len(s), i+linkTextMax)], '>'); end > 1 {
				t := s[i+1 : i+end]
				if autolink(t) {
					p.flush(st, url)
					p.buf.WriteString(t)
					dest := t
					if !strings.Contains(t, ":") {
						dest = "mailto:" + t
					}
					p.flush(st|Link, dest)
					i += end + 1
					continue
				}
			}
		case (c == 'h' || c == 'w') && url == "" && st&CodeSpan == 0 && (i == 0 || !isWordByte(s[i-1])):
			if n := bareURL(s[i:]); n > 0 {
				p.flush(st, url)
				t := s[i : i+n]
				p.buf.WriteString(t)
				if strings.HasPrefix(t, "www.") {
					t = "https://" + t
				}
				p.flush(st|Link, t)
				i += n
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		p.buf.WriteString(s[i : i+size])
		i += size
	}
	p.flush(st, url)
}

// emphasis finds the closer of a delimiter run of n of s[i] at i: where the
// closer starts, and the style the pair adds.
func (p *inline) emphasis(s string, i, n, depth int, miss *misses) (end int, add Style, ok bool) {
	c := s[i]
	if depth >= maxDepth || n > 3 || c == '~' && n != 2 || !leftFlanking(s, i, n) {
		return 0, 0, false
	}
	k, add := n, Emphasis
	switch {
	case c == '~':
		add = Strike
	case n == 2:
		add = Strong
	case n == 3:
		add = Strong | Emphasis
	}
	ci := strings.IndexByte("*_~", c)
	if miss.delim[ci][k] != 0 && i >= miss.delim[ci][k] {
		return 0, 0, false
	}
	for j := i + n + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '`':
			// A code span hides its delimiters.
			m := run(s, j, '`')
			if e := closeTicks(s, j+m, m); e >= 0 {
				j = e + m - 1
			} else {
				j += m - 1
			}
		case c:
			m := run(s, j, c)
			if m == k && rightFlanking(s, j, m) {
				return j, add, true
			}
			j += m - 1
		}
	}
	miss.delim[ci][k] = i + 1
	return 0, 0, false
}

// run is the length of the run of c at s[i].
func run(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// closeTicks finds a run of exactly n backticks from i, or -1.
func closeTicks(s string, i, n int) int {
	for i < len(s) {
		j := strings.IndexByte(s[i:], '`')
		if j < 0 {
			return -1
		}
		j += i
		m := run(s, j, '`')
		if m == n {
			return j
		}
		i = j + m
	}
	return -1
}

// before and after are the runes either side of a delimiter run, a space at
// the ends of the text.
func before(s string, i int) rune {
	if i == 0 {
		return ' '
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}

func after(s string, i int) rune {
	if i >= len(s) {
		return ' '
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}

// leftFlanking reports a run that can open: text follows it, and an
// underscore is not inside a word, so snake_case names stay as they are.
func leftFlanking(s string, i, n int) bool {
	a, b := after(s, i+n), before(s, i)
	if unicode.IsSpace(a) {
		return false
	}
	if unicode.IsPunct(a) && !unicode.IsSpace(b) && !unicode.IsPunct(b) {
		return false
	}
	return s[i] != '_' || !isWordRune(b)
}

// rightFlanking reports a run that can close.
func rightFlanking(s string, i, n int) bool {
	b, a := before(s, i), after(s, i+n)
	if unicode.IsSpace(b) {
		return false
	}
	if unicode.IsPunct(b) && !unicode.IsSpace(a) && !unicode.IsPunct(a) {
		return false
	}
	return s[i] != '_' || !isWordRune(a)
}

// linkTextMax bounds the search for the bracket that ends a link's text, so
// a text of many open brackets costs at most this much per bracket.
const linkTextMax = 1 << 10

// link reads [text](dest "title") at s[i]: where the text ends, the
// destination, where the link ends, and how many bytes the search read. A
// text with no destination after it is not a link.
func link(s string, i int) (end int, dest string, next, read int, ok bool) {
	depth := 0
	end = -1
	read = min(len(s), i+linkTextMax) - i
	for j := i; j < min(len(s), i+linkTextMax); j++ {
		switch s[j] {
		case '\\':
			j++
		case '`':
			m := run(s, j, '`')
			if e := closeTicks(s, j+m, m); e >= 0 {
				j = e + m - 1
			} else {
				j += m - 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = j
				read = j - i + 1
				j = len(s)
			}
		}
	}
	if end < 0 || end+1 >= len(s) || s[end+1] != '(' {
		return end, "", 0, read, false
	}
	j := end + 2
	for j < len(s) && s[j] == ' ' {
		j++
	}
	if j < len(s) && s[j] == '<' {
		k := strings.IndexAny(s[j:min(len(s), j+linkTextMax)], ">\n")
		if k < 0 || s[j+k] != '>' {
			return end, "", 0, read, false
		}
		dest = s[j+1 : j+k]
		j += k + 1
	} else {
		start, parens := j, 0
		for ; j < min(len(s), start+linkTextMax); j++ {
			if c := s[j]; c == ' ' || c == '\n' || c < 0x20 {
				break
			} else if c == '\\' {
				j++
			} else if c == '(' {
				parens++
			} else if c == ')' {
				if parens == 0 {
					break
				}
				parens--
			}
		}
		dest = s[start:min(j, len(s))]
		read += j - start
	}
	// A title in quotes may follow.
	for j < len(s) && (s[j] == ' ' || s[j] == '\n') {
		j++
	}
	if j < len(s) && (s[j] == '"' || s[j] == '\'') {
		q := s[j]
		k := strings.IndexByte(s[j+1:min(len(s), j+1+linkTextMax)], q)
		read += min(len(s)-j-1, linkTextMax)
		if k < 0 {
			return end, "", 0, read, false
		}
		j += k + 2
		for j < len(s) && s[j] == ' ' {
			j++
		}
	}
	if j >= len(s) || s[j] != ')' {
		return end, "", 0, read, false
	}
	return end, dest, j + 1, read, true
}

// autolink reports the inside of <...> that is a URL or an email address.
func autolink(t string) bool {
	if strings.ContainsAny(t, " <>\n") {
		return false
	}
	if scheme, rest, ok := strings.Cut(t, ":"); ok && len(scheme) >= 2 && len(scheme) <= 32 && rest != "" {
		for k := 0; k < len(scheme); k++ {
			if !isWordByte(scheme[k]) && scheme[k] != '+' && scheme[k] != '.' && scheme[k] != '-' {
				return false
			}
		}
		return true
	}
	at := strings.IndexByte(t, '@')
	return at > 0 && strings.IndexByte(t[at:], '.') > 0
}

// bareURL is the length of a URL at the start of s with no markup around
// it, http://, https:// or www., without the punctuation that ends a
// sentence after it. It is 0 when s does not start with one.
func bareURL(s string) int {
	var rest string
	switch {
	case strings.HasPrefix(s, "https://"):
		rest = s[8:]
	case strings.HasPrefix(s, "http://"):
		rest = s[7:]
	case strings.HasPrefix(s, "www."):
		rest = s[4:]
	default:
		return 0
	}
	n := len(s) - len(rest)
	for n < len(s) {
		c := s[n]
		if c <= ' ' || c == '<' || c == '`' || c == 0x7f {
			break
		}
		n++
	}
	// Trailing punctuation belongs to the sentence, and a closing
	// parenthesis only when the URL opened none.
	for n > len(s)-len(rest) {
		c := s[n-1]
		if strings.IndexByte(".,:;!?\"'*_~", c) >= 0 {
			n--
			continue
		}
		if c == ')' && strings.Count(s[:n], "(") < strings.Count(s[:n], ")") {
			n--
			continue
		}
		break
	}
	if n == len(s)-len(rest) || strings.IndexByte(s[len(s)-len(rest):n], '.') < 0 && !strings.HasPrefix(s[len(s)-len(rest):n], "localhost") {
		return 0
	}
	return n
}

// isPunct reports an ASCII punctuation character, which a backslash
// escapes.
func isPunct(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

// isWordByte reports an ASCII letter, digit or underscore.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// isWordRune reports a letter or digit.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
