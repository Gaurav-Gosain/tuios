package tmuxcompat

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Format expansion: tmux's format language, ported from format.c of tmux 3.4
// for the parts a tool driving tmux reads.
//
// Supported: #{name}, the one-letter aliases (#D #F #H #h #I #P #S #T #W),
// the escapes ##, #, and #}, conditionals #{?cond,then,else}, the
// comparisons ==, !=, <, >, <=, >=, || and &&, and the modifiers m (glob or
// regular expression match, with the r and i flags), l (literal), b and d
// (base and directory name), =N and =/N/marker (truncate), pN (pad), n
// (length), w (width), q (quote for the shell), E and T (expand again),
// t (time, with p for the short form), a (character) and s/pattern/with/flags
// (regular expression substitution). Several modifiers are separated by ";".
//
// Not supported, and reported as missing: the loops (S, W, P, L), N, C, c,
// e (arithmetic), q/e, and the strftime form of t. #(command) runs no
// command and is passed through, as are #[style] blocks, which mean
// something only to tmux's own status line.
//
// A variable the context does not hold expands to the empty string, as in
// tmux, and is reported back so the caller can log it.

// shortAliases are tmux's one-letter format aliases.
var shortAliases = map[byte]string{
	'D': "pane_id",
	'F': "window_flags",
	'H': "host",
	'h': "host_short",
	'I': "window_index",
	'P': "pane_index",
	'S': "session_name",
	'T': "pane_title",
	'W': "window_name",
}

// formatLoopLimit stops a format that expands itself (#{E:} of a value that
// holds the same #{E:}) the way tmux's FORMAT_LOOP_LIMIT does.
const formatLoopLimit = 100

// Expand expands format against vars. It returns the text and the names of
// the variables it had no value for, each once, in the order met.
func Expand(format string, vars map[string]string) (string, []string) {
	return expandWith(format, vars, nil)
}

// expandWith is Expand with more, consulted for a name vars does not hold:
// the values that cost a daemon call, read only when a format asks.
func expandWith(format string, vars map[string]string, more func(string) (string, bool)) (string, []string) {
	e := expander{vars: vars, more: more, seen: map[string]bool{}}
	return e.expand(format), e.missing
}

type expander struct {
	vars    map[string]string
	more    func(string) (string, bool)
	missing []string
	seen    map[string]bool
	loop    int
}

// miss records name as a value the format asked for and did not get.
func (e *expander) miss(name string) {
	if !e.seen[name] {
		e.seen[name] = true
		e.missing = append(e.missing, name)
	}
}

func (e *expander) find(name string) (string, bool) {
	if v, ok := e.vars[name]; ok {
		return v, true
	}
	if e.more != nil {
		return e.more(name)
	}
	return "", false
}

func (e *expander) expand(s string) string {
	e.loop++
	defer func() { e.loop-- }()
	if e.loop >= formatLoopLimit {
		e.miss("format loop limit")
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '#' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		next := s[i+1]
		switch {
		case next == '#' || next == ',' || next == '}':
			b.WriteByte(next)
			i++
		case next == '{':
			end := formatSkip(s[i:], "}")
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			b.WriteString(e.replace(s[i+2 : i+end]))
			i += end
		case shortAliases[next] != "":
			b.WriteString(e.lookup(shortAliases[next]))
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// lookup is a variable's value, "" and recorded as missing when there is none.
func (e *expander) lookup(name string) string {
	v, ok := e.find(name)
	if !ok {
		e.miss(name)
	}
	return v
}

// formatSkip is tmux's format_skip: the index of the first byte of s in end
// that is outside every nested #{...} and not escaped by "#", or -1.
func formatSkip(s, end string) int {
	brackets := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i+1 < len(s) && s[i+1] == '{' {
			brackets++
		}
		if s[i] == '#' && i+1 < len(s) && strings.IndexByte(",#{}:", s[i+1]) >= 0 {
			i++
			continue
		}
		if s[i] == '}' {
			brackets--
		}
		if strings.IndexByte(end, s[i]) >= 0 && brackets == 0 {
			return i
		}
	}
	return -1
}

// modifier is one modifier of a #{...}: its name and arguments.
type modifier struct {
	name string
	args []string
}

// isEnd reports whether key[i] ends a modifier.
func isEnd(key string, i int) bool {
	return i < len(key) && (key[i] == ';' || key[i] == ':')
}

// isPunct is C's ispunct for ASCII.
func isPunct(c byte) bool {
	return c > ' ' && c < 0x7f && !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
}

// modifiers reads the modifier list at the front of key, as
// format_build_modifiers does, and returns it with the rest of key. A key
// with no complete list before a ":" has no modifiers and is returned whole.
func (e *expander) modifiers(key string) ([]modifier, string) {
	var list []modifier
	cp := 0
	for cp < len(key) && key[cp] != ':' {
		if key[cp] == ';' {
			cp++
		}
		if cp >= len(key) {
			break
		}
		c := key[cp]
		if strings.IndexByte("labcdnwETSWPL<>", c) >= 0 && isEnd(key, cp+1) {
			list = append(list, modifier{name: key[cp : cp+1]})
			cp++
			continue
		}
		if cp+2 <= len(key) && isEnd(key, cp+2) {
			switch two := key[cp : cp+2]; two {
			case "||", "&&", "!=", "==", "<=", ">=":
				list = append(list, modifier{name: two})
				cp += 2
				continue
			}
		}
		if strings.IndexByte("mCNst=peq", c) < 0 {
			break
		}
		if isEnd(key, cp+1) {
			list = append(list, modifier{name: key[cp : cp+1]})
			cp++
			continue
		}
		if cp+1 >= len(key) {
			break
		}
		if !isPunct(key[cp+1]) || key[cp+1] == '-' {
			end := formatSkip(key[cp+1:], ":;")
			if end < 0 {
				break
			}
			end += cp + 1
			list = append(list, modifier{name: key[cp : cp+1], args: []string{e.expand(key[cp+1 : end])}})
			cp = end
			continue
		}
		// Arguments between a wrapper character: s/a/b/, =/5/..., m/ri.
		wrap := key[cp+1]
		m := modifier{name: key[cp : cp+1]}
		cp++
		for cp < len(key) {
			if key[cp] == wrap && isEnd(key, cp+1) {
				cp++
				break
			}
			end := formatSkip(key[cp+1:], string(wrap)+";:")
			if end < 0 {
				break
			}
			end += cp + 1
			m.args = append(m.args, e.expand(key[cp+1:end]))
			cp = end
			if isEnd(key, cp) {
				break
			}
		}
		list = append(list, m)
	}
	if cp >= len(key) || key[cp] != ':' {
		return nil, key
	}
	return list, key[cp+1:]
}

// valueFlags are the modifiers that change a looked-up value.
type valueFlags struct {
	base, dir, quote, timeString, pretty bool
}

// replace expands the inside of one #{...}: format_replace.
func (e *expander) replace(key string) string {
	mods, copy := e.modifiers(key)
	var cmp *modifier
	var subs []modifier
	var vf valueFlags
	limit, width := 0, 0
	marker, hasMarker := "", false
	var literal, char, again, length, cells bool
	for i := range mods {
		m := &mods[i]
		switch m.name {
		case "m", "<", ">", "||", "&&", "==", "!=", "<=", ">=":
			cmp = m
		case "s":
			if len(m.args) >= 2 {
				subs = append(subs, *m)
			}
		case "=":
			if len(m.args) >= 1 {
				limit, _ = strconv.Atoi(m.args[0])
				if len(m.args) >= 2 {
					marker, hasMarker = m.args[1], true
				}
			}
		case "p":
			if len(m.args) >= 1 {
				width, _ = strconv.Atoi(m.args[0])
			}
		case "w":
			cells = true
		case "n":
			length = true
		case "l":
			literal = true
		case "a":
			char = true
		case "b":
			vf.base = true
		case "d":
			vf.dir = true
		case "t":
			vf.timeString = true
			if len(m.args) >= 1 && strings.Contains(m.args[0], "p") {
				vf.pretty = true
			} else if len(m.args) >= 2 && strings.Contains(m.args[0], "f") {
				e.miss("modifier t/f")
			}
		case "q":
			if len(m.args) < 1 {
				vf.quote = true
			} else {
				e.miss("modifier q/" + m.args[0])
			}
		case "E", "T":
			again = true
		default:
			// The loops, N, C, c and e.
			e.miss("modifier " + m.name)
			return ""
		}
	}

	var value string
	switch {
	case literal:
		value = formatUnescape(copy)
	case char:
		n, err := strconv.Atoi(e.expand(copy))
		if err == nil && n >= 32 && n <= 126 {
			value = string(rune(n))
		}
	case cmp != nil:
		cut := formatSkip(copy, ",")
		if cut < 0 {
			return ""
		}
		left, right := e.expand(copy[:cut]), e.expand(copy[cut+1:])
		value = compare(cmp, left, right)
	case strings.HasPrefix(copy, "?"):
		cut := formatSkip(copy[1:], ",")
		if cut < 0 {
			return ""
		}
		cond := copy[1 : 1+cut]
		found, ok := e.find(cond)
		if ok {
			found = vf.apply(found, &ok)
		} else {
			found = e.expand(cond)
			if found == cond {
				if !strings.Contains(cond, "#") && cond != "" {
					e.miss(cond)
				}
				found = ""
			}
		}
		rest := copy[2+cut:]
		cut = formatSkip(rest, ",")
		if cut < 0 {
			return ""
		}
		if formatTrue(found) {
			value = e.expand(rest[:cut])
		} else {
			value = e.expand(rest[cut+1:])
		}
	case strings.Contains(copy, "#{"):
		// An inner format: #{=5:#{pane_title}}.
		value = e.expand(copy)
	default:
		v, ok := e.find(copy)
		if ok {
			v = vf.apply(v, &ok)
		}
		if !ok {
			e.miss(copy)
		}
		value = v
	}

	if again {
		value = e.expand(value)
	}
	for _, m := range subs {
		icase := len(m.args) >= 3 && strings.Contains(m.args[2], "i")
		value = regsub(e.expand(m.args[0]), e.expand(m.args[1]), value, icase)
	}
	if limit > 0 {
		if cut := ansi.Truncate(value, limit, ""); cut != value && hasMarker {
			value = cut + marker
		} else {
			value = cut
		}
	} else if limit < 0 {
		cut := value
		if w := ansi.StringWidth(value); w > -limit {
			cut = ansi.TruncateLeft(value, w+limit, "")
		}
		if cut != value && hasMarker {
			value = marker + cut
		} else {
			value = cut
		}
	}
	if pad := abs(width) - ansi.StringWidth(value); pad > 0 {
		if width > 0 {
			value += strings.Repeat(" ", pad)
		} else {
			value = strings.Repeat(" ", pad) + value
		}
	}
	if length {
		value = strconv.Itoa(len(value))
	} else if cells {
		value = strconv.Itoa(ansi.StringWidth(value))
	}
	return value
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// apply changes a found value as format_find does for the b, d, q and t
// modifiers. t of a value that is not a positive number finds nothing, so ok
// turns false.
func (vf valueFlags) apply(v string, ok *bool) string {
	if vf.timeString {
		t, err := strconv.ParseInt(v, 10, 64)
		if err != nil || t <= 0 {
			*ok = false
			return ""
		}
		at := time.Unix(t, 0)
		if vf.pretty {
			return prettyTime(at, time.Now())
		}
		return at.Format("Mon Jan _2 15:04:05 2006")
	}
	if vf.base {
		v = filepath.Base(v)
	}
	if vf.dir {
		v = filepath.Dir(v)
	}
	if vf.quote {
		v = quoteShell(v)
	}
	return v
}

// prettyTime is format_pretty_time without seconds.
func prettyTime(t, now time.Time) string {
	age := now.Sub(t)
	switch {
	case age < 24*time.Hour:
		return t.Format("15:04")
	case t.Year() == now.Year() && t.Month() == now.Month() || age < 28*24*time.Hour:
		return t.Format("Mon02")
	case t.Year() == now.Year() && t.Month() < now.Month() ||
		t.Year() == now.Year()-1 && t.Month() > now.Month():
		return t.Format("02Jan")
	}
	return t.Format("Jan06")
}

// quoteShell escapes the characters a shell reads specially, as tmux's
// format_quote_shell does.
func quoteShell(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte("|&;<>()$`\\\"'*?[# =%", s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// formatTrue is tmux's format_true: not empty and not "0".
func formatTrue(s string) bool {
	return s != "" && s != "0"
}

// compare answers a comparison modifier.
func compare(m *modifier, left, right string) string {
	switch m.name {
	case "||":
		return boolString(formatTrue(left) || formatTrue(right))
	case "&&":
		return boolString(formatTrue(left) && formatTrue(right))
	case "==":
		return boolString(left == right)
	case "!=":
		return boolString(left != right)
	case "<":
		return boolString(left < right)
	case ">":
		return boolString(left > right)
	case "<=":
		return boolString(left <= right)
	case ">=":
		return boolString(left >= right)
	}
	// m: a glob, or with the r flag a regular expression, matched against
	// right. The i flag ignores case.
	flags := ""
	if len(m.args) > 0 {
		flags = m.args[0]
	}
	pattern := left
	if !strings.Contains(flags, "r") {
		pattern = "^" + globRegexp(left) + "$"
	}
	if strings.Contains(flags, "i") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile("(?s)" + pattern)
	if err != nil {
		return "0"
	}
	return boolString(re.MatchString(right))
}

// globRegexp turns an fnmatch pattern (no FNM_PATHNAME, so * matches /) into
// a regular expression.
func globRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '\\':
			if i+1 < len(glob) {
				i++
				b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
			} else {
				b.WriteString(`\\`)
			}
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	return b.String()
}

// regsub replaces the matches of pattern in text with with, where \0 to \9
// name the match and its groups. It is a port of tmux's regsub, quirks
// included: an empty match is skipped when it follows another, and a
// pattern anchored with ^ stops after its first match. A pattern that does
// not compile leaves text as it is.
func regsub(pattern, with, text string, icase bool) string {
	if text == "" {
		return ""
	}
	expr := pattern
	if icase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return text
	}
	var b strings.Builder
	start, last, end := 0, 0, len(text)
	empty := false
	for start <= end {
		m := re.FindStringSubmatchIndex(text[start:])
		if m == nil {
			b.WriteString(text[start:end])
			break
		}
		b.WriteString(text[last : start+m[0]])
		if empty || start+m[0] != last || m[0] != m[1] {
			regsubExpand(&b, with, text[start:], m)
			last = start + m[1]
			start += m[1]
			empty = false
		} else {
			last = start + m[1]
			start += m[1] + 1
			empty = true
		}
		if strings.HasPrefix(pattern, "^") {
			if start <= end {
				b.WriteString(text[start:end])
			}
			break
		}
	}
	return b.String()
}

// regsubExpand writes with for one match: \N is group N when that group
// matched something, and a backslash before anything else is dropped.
func regsubExpand(b *strings.Builder, with, text string, m []int) {
	for i := 0; i < len(with); i++ {
		if with[i] == '\\' {
			i++
			if i >= len(with) {
				return
			}
			if c := with[i]; c >= '0' && c <= '9' {
				n := int(c - '0')
				if 2*n+1 < len(m) && m[2*n] >= 0 && m[2*n] != m[2*n+1] {
					b.WriteString(text[m[2*n]:m[2*n+1]])
					continue
				}
			}
		}
		b.WriteByte(with[i])
	}
}

// formatUnescape is tmux's format_unescape: the escapes outside nested
// #{...} lose their "#".
func formatUnescape(s string) string {
	var b strings.Builder
	brackets := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i+1 < len(s) && s[i+1] == '{' {
			brackets++
		}
		if brackets == 0 && s[i] == '#' && i+1 < len(s) && strings.IndexByte(",#{}:", s[i+1]) >= 0 {
			i++
			b.WriteByte(s[i])
			continue
		}
		if s[i] == '}' {
			brackets--
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
