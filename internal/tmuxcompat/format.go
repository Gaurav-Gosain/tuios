package tmuxcompat

import (
	"strings"
)

// Format expansion: the part of tmux's format language the shim answers.
//
// Supported: #{name}, the one-letter aliases (#D #F #H #h #I #P #S #T #W), ##
// for a literal #, #{?cond,then,else} with nested formats, and the
// comparisons #{==:a,b} and #{!=:a,b}. A variable the context does not hold
// expands to the empty string, which is what tmux does too, and is reported
// back so the caller can log it. #[style] blocks are passed through, since
// they only mean something to tmux's own status line.

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

// Expand expands format against vars. It returns the text and the names of
// the variables it had no value for, each once, in the order met.
func Expand(format string, vars map[string]string) (string, []string) {
	e := expander{vars: vars, seen: map[string]bool{}}
	return e.expand(format), e.missing
}

type expander struct {
	vars    map[string]string
	missing []string
	seen    map[string]bool
}

func (e *expander) lookup(name string) string {
	v, ok := e.vars[name]
	if !ok && !e.seen[name] {
		e.seen[name] = true
		e.missing = append(e.missing, name)
	}
	return v
}

func (e *expander) expand(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '#' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		next := s[i+1]
		switch {
		case next == '#':
			b.WriteByte('#')
			i++
		case next == ',' || next == '}':
			// #, and #} escape the characters that separate conditional
			// branches.
			b.WriteByte(next)
			i++
		case next == '{':
			end := matchBrace(s, i+1)
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			b.WriteString(e.block(s[i+2 : end]))
			i = end
		case shortAliases[next] != "":
			b.WriteString(e.lookup(shortAliases[next]))
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// block expands the inside of one #{...}.
func (e *expander) block(inner string) string {
	switch {
	case strings.HasPrefix(inner, "?"):
		parts := splitTop(inner[1:])
		if len(parts) == 0 {
			return ""
		}
		cond := e.condition(parts[0])
		if cond {
			if len(parts) > 1 {
				return e.expand(parts[1])
			}
			return ""
		}
		if len(parts) > 2 {
			return e.expand(parts[2])
		}
		return ""
	case strings.HasPrefix(inner, "==:") || strings.HasPrefix(inner, "!=:"):
		parts := splitTop(inner[3:])
		if len(parts) != 2 {
			return ""
		}
		eq := e.expand(parts[0]) == e.expand(parts[1])
		if strings.HasPrefix(inner, "!=") {
			eq = !eq
		}
		return boolString(eq)
	}
	if strings.ContainsAny(inner, ":{#") {
		// A modifier (#{t:...}, #{s/a/b/:...}, #{=5:...}) or a nested format
		// the shim does not implement. It is reported by its whole text.
		name := inner
		if !e.seen[name] {
			e.seen[name] = true
			e.missing = append(e.missing, name)
		}
		return ""
	}
	return e.lookup(inner)
}

// condition evaluates a conditional's test: a variable name, or a nested
// format, is true when it expands to something other than "" or "0".
func (e *expander) condition(c string) bool {
	var v string
	if strings.Contains(c, "#") {
		v = e.expand(c)
	} else if strings.HasPrefix(c, "==:") || strings.HasPrefix(c, "!=:") {
		v = e.block(c)
	} else {
		v = e.lookup(c)
	}
	return v != "" && v != "0"
}

// matchBrace returns the index of the "}" closing the "{" at open, counting
// nested #{ blocks, or -1.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '#':
			if i+1 < len(s) && (s[i+1] == '#' || s[i+1] == ',' || s[i+1] == '}') {
				i++
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTop splits s on the commas outside nested #{...} blocks.
func splitTop(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '#':
			if i+1 < len(s) && (s[i+1] == '#' || s[i+1] == ',' || s[i+1] == '}') {
				i++
			}
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
