package tape

import (
	"strconv"
	"strings"
)

// Project tape scope values. See ProjectHeader.Scope.
const (
	// ScopeSession builds the tape in a session named after the project (the
	// default). The session is the durable artifact; the tape constructs it once.
	ScopeSession = "session"
	// ScopeCurrent applies the tape to the current session, starting from the
	// focused window. Best-effort: it composes with whatever state exists.
	ScopeCurrent = "current"
)

// ProjectHeader is the declarative header of a .tuios.tape. It is a small set of
// directives that may appear only in a leading block, before any action command:
//
//	Session "name"      target session name (default: project directory basename)
//	Scope session|current   default session
//	Workspace 2         which workspace inside the session to build in (0 = none)
//	Require "command"   skip-with-notice if a binary is missing (repeatable)
//
// Everything below the header is the existing tape language. Parsing the header
// is a cheap prefix scan that executes nothing, so the review dialog can render
// "this will build session <name>" without running a line of the tape.
type ProjectHeader struct {
	Session   string
	Scope     string
	Workspace int
	Requires  []string
	// HasHeader is true when at least one recognized directive was parsed. It
	// lets callers tell an explicit header from the defaults applied to a tape
	// that has none.
	HasHeader bool
}

// headerKeywords is the set of leading directive names, matched case-insensitively.
var headerKeywords = map[string]bool{
	"session":   true,
	"scope":     true,
	"workspace": true,
	"require":   true,
}

// ParseProjectHeader splits a tape's content into its declarative header and the
// remaining body. The header is the run of leading lines that are blank, a
// comment, or a recognized directive; the body is everything from the first
// action command onward, returned verbatim so the existing lexer parses it
// unchanged.
//
// It never executes anything and is robust to hostile input: an unrecognized or
// malformed directive simply ends the header and starts the body.
func ParseProjectHeader(content string) (ProjectHeader, string) {
	h := ProjectHeader{Scope: ScopeSession}

	lines := strings.Split(content, "\n")
	bodyStart := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			// Blank lines and comments are allowed inside the header block and do
			// not end it. If no directive ever follows, they fall into the body
			// below (bodyStart stays at the first such line only when it precedes
			// no directive), so a bare comment tape still runs.
			bodyStart = i + 1
			continue
		}

		keyword, rest := splitFirstField(trimmed)
		if !headerKeywords[strings.ToLower(keyword)] {
			// First real command: the header ends here.
			bodyStart = i
			break
		}

		applyHeaderDirective(&h, keyword, rest)
		h.HasHeader = true
		bodyStart = i + 1
	}

	body := strings.Join(lines[bodyStart:], "\n")
	return h, body
}

// applyHeaderDirective folds one directive into the header. Unknown values fall
// back to a sane default rather than erroring, so a typo never makes a tape
// unreviewable.
func applyHeaderDirective(h *ProjectHeader, keyword, rest string) {
	switch strings.ToLower(keyword) {
	case "session":
		h.Session = unquote(strings.TrimSpace(rest))
	case "scope":
		switch strings.ToLower(strings.TrimSpace(unquote(rest))) {
		case ScopeCurrent:
			h.Scope = ScopeCurrent
		default:
			h.Scope = ScopeSession
		}
	case "workspace":
		if n, err := strconv.Atoi(strings.TrimSpace(unquote(rest))); err == nil && n >= 0 {
			h.Workspace = n
		}
	case "require":
		if cmd := unquote(strings.TrimSpace(rest)); cmd != "" {
			h.Requires = append(h.Requires, cmd)
		}
	}
}

// splitFirstField splits a line into its first whitespace-delimited token and
// the untrimmed remainder.
func splitFirstField(s string) (string, string) {
	i := strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

// unquote strips a single pair of surrounding double or single quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
