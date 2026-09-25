package agentproto

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// rlo and pdf are the bidi override and its end, built from their code points
// so the source holds no invisible character.
var (
	rlo = string(rune(0x202e))
	pdf = string(rune(0x202c))
)

// TestCleanRemovesEverySequence: nothing the agent sends reaches the pane as
// an escape sequence or a control character, so it cannot set the pane's
// agent state (OSC 9, 777, the tuios OSC), its title, the clipboard, or move
// the cursor over what was written.
func TestCleanRemovesEverySequence(t *testing.T) {
	cases := map[string]string{
		"plain\ttext\nline":                        "plain\ttext\nline",
		"a\x1b]0;evil title\x07b":                  "a]0;evil titleb",
		"x\x1b]9;4;3\x1b\\y":                       "x]9;4;3\\y",
		"\x1b[2J\x1b[Hcleared":                     "[2J[Hcleared",
		"cr\roverwrite":                            "croverwrite",
		"c1\u009b31mred":                           "c131mred",
		"bidi" + rlo + "evil" + pdf:                "bidievil",
		"bad \xff utf8":                            "bad " + string(utf8.RuneError) + " utf8",
		"\x1b]52;c;ZXZpbA==\x07clip":               "]52;c;ZXZpbA==clip",
		"bell\x07 del\x7f nul\x00 backspace\x08 x": "bell del nul backspace x",
	}
	for in, want := range cases {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRendererShowsNoAgentEscape renders every event kind from strings full of
// escape sequences and checks the only escapes in the output are the
// transcript's own SGR.
func TestRendererShowsNoAgentEscape(t *testing.T) {
	evil := "\x1b]2;pwned\x07\x1b[31m"
	r := newRenderer()
	old := evil
	var out strings.Builder
	for _, ev := range []Event{
		Text{Text: evil + "reply"},
		Text{Text: evil + "thought", Thought: true},
		Tool{ID: evil, Title: evil, Kind: evil, Status: ToolDone, Text: evil, Diffs: []Diff{{Path: evil, Old: &old, New: evil + "\n"}}},
		Tool{ID: "u", Title: "t", Status: ToolDone, Diffs: []Diff{{Path: evil, Unified: evil}}},
		Plan{Entries: []PlanEntry{{Content: evil, Status: "completed"}}},
		Notice{Text: evil, Error: true},
	} {
		out.WriteString(r.event(ev))
	}
	perm := NewPermission(Tool{ID: "p", Title: evil, Input: map[string]string{"k": evil}}, []Option{{Label: evil}}, func(int) {}, func() {})
	perm.Reason, perm.Detail = evil, evil
	out.WriteString(r.permission(perm))
	out.WriteString(r.answered(perm, 0, evil))
	out.WriteString(r.prompt(evil))
	out.WriteString(r.turnEnd(TurnResult{Stop: StopFailed, Detail: evil}))
	assertOnlyOwnSGR(t, out.String())
}

// assertOnlyOwnSGR fails on any escape in s that is not one of the
// transcript's SGR sequences.
func assertOnlyOwnSGR(t *testing.T, s string) {
	t.Helper()
	for _, sgr := range []string{sgrReset, sgrBold, sgrDim, sgrRed, sgrGreen, sgrYellow, sgrCyan} {
		s = strings.ReplaceAll(s, sgr, "")
	}
	if i := strings.IndexByte(s, 0x1b); i >= 0 {
		t.Fatalf("an escape reached the transcript at %d: %q", i, s[max(0, i-10):min(len(s), i+20)])
	}
	if strings.Contains(s, "\x07") || strings.Contains(s, "\r") {
		t.Fatalf("a control character reached the transcript: %q", s)
	}
}

// TestInboxLine is the rule for what the Inbox may answer.
func TestInboxLine(t *testing.T) {
	once := []Option{{Label: "Allow", Decision: DecisionOnce}, {Label: "Deny", Decision: DecisionDeny}}
	old := "a"
	cases := []struct {
		name string
		tool Tool
		opts []Option
		want string
	}{
		{"a command", Tool{Title: "go test ./...", Kind: "execute", Input: map[string]string{"command": "go test ./..."}}, once, "approve execute: go test ./..."},
		{"no input", Tool{Title: "Fetch https://example.com", Kind: "fetch"}, once, "approve fetch: Fetch https://example.com"},
		{"a description is not the call", Tool{Title: "ls", Kind: "execute", Input: map[string]string{"command": "ls", "description": "list files"}}, once, "approve execute: ls"},
		{"no kind", Tool{Title: "x"}, once, "approve tool: x"},
		{"input the title does not show", Tool{Title: "Run command", Kind: "execute", Input: map[string]string{"command": "rm -rf /"}}, once, ""},
		{"input that is not a string", Tool{Title: "ls", OtherInput: true}, once, ""},
		{"a diff", Tool{Title: "Edit a.go", Diffs: []Diff{{Path: "a.go", Old: &old, New: "b"}}}, once, ""},
		{"output", Tool{Title: "x", Text: "more"}, once, ""},
		{"a terminal", Tool{Title: "x", Terminal: true}, once, ""},
		{"no title", Tool{ID: "t1"}, once, ""},
		{"no allow once", Tool{Title: "x"}, []Option{{Label: "Always", Decision: ""}}, ""},
		{"too long", Tool{Title: strings.Repeat("a", 300)}, once, ""},
		{"a secret the Inbox would mask", Tool{Title: "curl -H 'Authorization: Bearer sk-abcdefghijklmnopqrstuvwxyz0123456789'"}, once, ""},
		{"whitespace the Inbox would collapse", Tool{Title: "echo  a"}, once, ""},
		{"a format character", Tool{Title: "ls " + rlo}, once, ""},
	}
	for _, tc := range cases {
		p := NewPermission(tc.tool, tc.opts, func(int) {}, func() {})
		if got := InboxLine(p); got != tc.want {
			t.Errorf("%s: InboxLine = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A request the Inbox cannot answer still reports a line, cleaned, which
	// the Inbox shows without answer keys.
	p := NewPermission(Tool{Title: "Run\x1b[2J  command", Kind: "execute", Input: map[string]string{"command": "rm -rf /"}}, once, func(int) {}, func() {})
	if got := StateLine(p); got != "approve execute: Run[2J command" {
		t.Errorf("StateLine = %q", got)
	}
}
