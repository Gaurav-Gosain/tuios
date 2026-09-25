package agentproto

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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

// TestToolLinesSayTheirState: a tool call is shown when it starts and when it
// ends, with its state as a word, and not on updates in between.
func TestToolLinesSayTheirState(t *testing.T) {
	r := newRenderer()
	var lines []string
	for _, st := range []string{ToolPending, ToolRunning, ToolRunning, ToolDone} {
		if s := ansi.Strip(r.event(Tool{ID: "t", Title: "go test", Kind: "execute", Status: st})); s != "" {
			lines = append(lines, strings.TrimSpace(s))
		}
	}
	want := []string{"[running] execute: go test", "[done] execute: go test"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("tool lines = %q, want %q", lines, want)
	}
	failed := ansi.Strip(newRenderer().event(Tool{ID: "t", Title: "x", Status: ToolFailed, Text: "exit code 1"}))
	if !strings.Contains(failed, "[failed] x") || !strings.Contains(failed, "exit code 1") {
		t.Errorf("a failed call reads %q", failed)
	}
}

// TestStreamingAndLineStarts: a reply streams on one line, and whatever comes
// next starts on a line of its own.
func TestStreamingAndLineStarts(t *testing.T) {
	r := newRenderer()
	got := r.event(Text{Text: "Hel"}) + r.event(Text{Text: "lo"}) + r.event(Notice{Text: "n"})
	if ansi.Strip(got) != "Hello\nnote: n\n" {
		t.Errorf("got %q", ansi.Strip(got))
	}
	r = newRenderer()
	got = r.event(Text{Text: "think", Thought: true}) + r.event(Text{Text: "reply"})
	if ansi.Strip(got) != "think\nreply" {
		t.Errorf("thought then reply = %q", ansi.Strip(got))
	}
}

func TestPlanShownWhenItChanges(t *testing.T) {
	r := newRenderer()
	p := Plan{Entries: []PlanEntry{{Content: "a", Status: "completed"}, {Content: "b", Status: "in_progress"}, {Content: "c", Status: "pending"}}}
	first := ansi.Strip(r.event(p))
	if first != "plan\n  [x] a\n  [>] b\n  [ ] c\n" {
		t.Errorf("plan = %q", first)
	}
	if again := r.event(p); again != "" {
		t.Errorf("an unchanged plan was shown again: %q", again)
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

func TestUnifiedHunks(t *testing.T) {
	a := strings.Split("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12", "\n")
	b := strings.Split("1\n2\n3\n4\nfive\n6\n7\n8\n9\n10\n11\n12\n13", "\n")
	got := strings.Join(unifiedHunks(a, b, 3), "\n")
	want := strings.Join([]string{
		"@@ -2,7 +2,7 @@", " 2", " 3", " 4", "-5", "+five", " 6", " 7", " 8",
		"@@ -10,3 +10,4 @@", " 10", " 11", " 12", "+13",
	}, "\n")
	if got != want {
		t.Errorf("hunks:\n%s\nwant:\n%s", got, want)
	}
	// A new file is all added, from /dev/null.
	lines := diffLines(Diff{Path: "n.go", New: "x\ny\n"})
	if strings.Join(lines, "|") != "--- /dev/null|+++ n.go|@@ -0,0 +1,2 @@|+x|+y" {
		t.Errorf("new file = %q", lines)
	}
}

// TestDiffIsBounded: a huge diff shows maxDiffLines lines and says how many
// more there are, and two texts too large to compare still diff.
func TestDiffIsBounded(t *testing.T) {
	var a, b []string
	for i := range 3000 {
		a = append(a, "old"+string(rune('a'+i%26)))
		b = append(b, "new"+string(rune('a'+i%26)))
	}
	oldText := strings.Join(a, "\n")
	out := ansi.Strip(renderDiffs([]Diff{{Path: "big", Old: &oldText, New: strings.Join(b, "\n")}}))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != maxDiffLines+1 || !strings.Contains(lines[len(lines)-1], "more diff lines") {
		t.Errorf("a big diff showed %d lines, last %q", len(lines), lines[len(lines)-1])
	}
	for _, l := range lines[2:maxDiffLines] {
		if l[0] != '+' && l[0] != '-' && l[0] != '@' {
			t.Fatalf("a diff line lost its sign: %q", l)
		}
	}
}
