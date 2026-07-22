package tape

import "testing"

func TestParseProjectHeaderFull(t *testing.T) {
	content := `# .tuios.tape for myproject
Session "my project"
Scope session
Workspace 2
Require "pnpm"
Require "nvim"

RenameWindow "edit"
Type "nvim ." Enter
`
	h, body := ParseProjectHeader(content)

	if !h.HasHeader {
		t.Fatalf("HasHeader = false, want true")
	}
	if h.Session != "my project" {
		t.Fatalf("Session = %q, want %q", h.Session, "my project")
	}
	if h.Scope != ScopeSession {
		t.Fatalf("Scope = %q, want %q", h.Scope, ScopeSession)
	}
	if h.Workspace != 2 {
		t.Fatalf("Workspace = %d, want 2", h.Workspace)
	}
	if len(h.Requires) != 2 || h.Requires[0] != "pnpm" || h.Requires[1] != "nvim" {
		t.Fatalf("Requires = %v, want [pnpm nvim]", h.Requires)
	}
	if got := firstLine(body); got != `RenameWindow "edit"` {
		t.Fatalf("body first line = %q, want the first action command", got)
	}
	if containsLine(body, "Session") {
		t.Fatalf("body still contains a header directive:\n%s", body)
	}
}

func TestParseProjectHeaderScopeCurrent(t *testing.T) {
	h, body := ParseProjectHeader("Scope current\nType \"make watch\" Enter\n")
	if h.Scope != ScopeCurrent {
		t.Fatalf("Scope = %q, want current", h.Scope)
	}
	if firstLine(body) != `Type "make watch" Enter` {
		t.Fatalf("body first line = %q", firstLine(body))
	}
}

func TestParseProjectHeaderNone(t *testing.T) {
	// A tape with no header keeps its whole content as the body and defaults to
	// session scope.
	content := "Type \"echo hi\" Enter\nSplit vertical\n"
	h, body := ParseProjectHeader(content)
	if h.HasHeader {
		t.Fatalf("HasHeader = true, want false for a headerless tape")
	}
	if h.Scope != ScopeSession {
		t.Fatalf("default Scope = %q, want session", h.Scope)
	}
	if firstLine(body) != `Type "echo hi" Enter` {
		t.Fatalf("body first line = %q, want the whole tape preserved", firstLine(body))
	}
}

func TestParseProjectHeaderUnknownScopeDefaults(t *testing.T) {
	h, _ := ParseProjectHeader("Scope sideways\nType \"x\" Enter\n")
	if h.Scope != ScopeSession {
		t.Fatalf("unknown Scope = %q, want fallback to session", h.Scope)
	}
}

func TestParseProjectHeaderStopsAtFirstCommand(t *testing.T) {
	// A directive appearing after an action command is not part of the header:
	// header directives must precede any action command.
	content := "Type \"x\" Enter\nSession \"late\"\n"
	h, _ := ParseProjectHeader(content)
	if h.Session != "" {
		t.Fatalf("Session = %q, want empty (directive after a command is body, not header)", h.Session)
	}
}

func firstLine(s string) string {
	for _, line := range splitLines(s) {
		if trimmed := trimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func containsLine(s, keyword string) bool {
	for _, line := range splitLines(s) {
		if len(line) >= len(keyword) && line[:len(keyword)] == keyword {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
