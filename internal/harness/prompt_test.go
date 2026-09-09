package harness

import "testing"

// TestRulePromptReadsTheMatchedLine is the claim behind a blocked agent's alert
// carrying the question it asked: the rule that matched has the line, cleaned
// of the box and the cursor mark the TUI painted around it.
func TestRulePromptReadsTheMatchedLine(t *testing.T) {
	reg, errs := Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	tail := []string{
		"╭──────────────────────────────────────────╮",
		"│ Do you want to make this edit to main.go? │",
		"│ ❯ 1. Yes                                  │",
		"│   2. No, and tell Claude what to do       │",
		"╰──────────────────────────────────────────╯",
	}
	state, rule, ok := reg.Classify("claude-code", tail)
	if !ok || state != "needs_input" {
		t.Fatalf("Classify = %q, %d, %v; want needs_input", state, rule, ok)
	}
	if got, want := reg.RulePrompt("claude-code", rule, tail), "Do you want to make this edit to main.go?"; got != want {
		t.Fatalf("RulePrompt = %q, want %q", got, want)
	}
	if got := reg.RuleKind("claude-code", rule); got != PromptKindApproval {
		t.Fatalf("RuleKind = %q, want %q", got, PromptKindApproval)
	}
	if got := reg.RulePrompt("claude-code", rule, nil); got != "" {
		t.Fatalf("RulePrompt with no tail = %q, want empty", got)
	}
}

// TestRuleKindGuessesFromTheRulesWords: a manifest that names no kind still
// gets one, from the words the rule itself carries.
func TestRuleKindGuessesFromTheRulesWords(t *testing.T) {
	reg, errs := Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	// The trust prompt: "Do you trust the files in this folder" is a question
	// wanting a yes, which the word trust marks as an approval.
	if got := reg.RuleKind("claude-code", 1); got != PromptKindApproval {
		t.Fatalf("trust rule kind = %q, want approval", got)
	}
	if got := reg.RuleKind("claude-code", 99); got != "" {
		t.Fatalf("a rule that does not exist has kind %q, want empty", got)
	}
}

// TestCleanPromptLineStripsChrome pins what is taken off a prompt line before
// it travels as a message.
func TestCleanPromptLineStripsChrome(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"│ Do you want to proceed? │", "Do you want to proceed?"},
		{"❯ 1. Yes", "1. Yes"},
		{"> Run this command?", "Run this command?"},
		{"\x07Bell\x00 question\t here", "Bell question here"},
		{"   ", ""},
		{"▁▂▃ spinner text", "spinner text"},
	} {
		if got := CleanPromptLine(tc.in); got != tc.want {
			t.Errorf("CleanPromptLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := make([]byte, 0, 400)
	for range 400 {
		long = append(long, 'x')
	}
	if got := CleanPromptLine(string(long)); len([]rune(got)) != maxPromptRunes {
		t.Errorf("a %d-rune line was cut to %d runes, want %d", len(long), len([]rune(got)), maxPromptRunes)
	}
}
