package tape

import "testing"

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
