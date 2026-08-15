package harness

import (
	"strings"
	"testing"
)

// The screen tier is the only channel carrying "this pane is waiting for a
// human" for a harness that paints its prompt and then goes silent. These are
// the rules that keep it from becoming the thing it replaced: a source confident
// about a screen it no longer understands.

func TestClassifyNamesNoStateWhenNothingMatches(t *testing.T) {
	r := testRegistry(t)
	if state, _, ok := r.Classify("claude-code", []string{"just some output", "$ "}); ok {
		t.Fatalf("classified an ordinary screen as %q; a rule that does not match must leave no opinion", state)
	}
}

func TestClassifyFindsABlockingPrompt(t *testing.T) {
	r := testRegistry(t)
	tail := []string{
		"  Edit file src/main.go",
		"",
		"Do you want to proceed?",
		"❯ 1. Yes",
		"  2. No, and tell Claude what to do differently",
	}
	state, _, ok := r.Classify("claude-code", tail)
	if !ok {
		t.Fatal("a blocking permission prompt matched no rule, which is the whole reason this tier exists")
	}
	if state != "needs_input" {
		t.Errorf("classified the prompt as %q, want needs_input", state)
	}
}

// A rule fires on the strings it names and not on the topic they belong to.
// Transcript history mentioning a past prompt must not read as a live one.
func TestClassifyIgnoresProseAboutPrompts(t *testing.T) {
	r := testRegistry(t)
	tail := []string{
		"I asked whether you wanted to proceed and you said yes.",
		"Now running the build.",
	}
	if state, _, ok := r.Classify("claude-code", tail); ok {
		t.Errorf("prose about a prompt classified as %q", state)
	}
}

func TestClassifyIsSilentForAHarnessWithRulesOff(t *testing.T) {
	r := testRegistry(t)
	for _, id := range r.IDs() {
		m := r.Lookup(id)
		if m.Screen.Enabled {
			continue
		}
		if lines := r.ScreenLines(id); lines != 0 {
			t.Errorf("%s has screen rules disabled but asks for %d lines; the tail read must not happen at all", id, lines)
		}
		if _, _, ok := r.Classify(id, []string{"Do you want to proceed?", "❯ 1. Yes"}); ok {
			t.Errorf("%s classified a screen while disabled", id)
		}
	}
}

// A rule naming neither All nor Any would match every screen the harness ever
// paints, which is a rule that says the pane is always blocked.
func TestRuleWithNoPredicatesMatchesNothing(t *testing.T) {
	rl := &ScreenRule{State: "needs_input"}
	if checkRule(rl, "anything at all", nil) {
		t.Fatal("a rule with no predicates matched; it would claim every pane")
	}
}

func TestNotPredicateVetoesAMatch(t *testing.T) {
	rl := &ScreenRule{State: "needs_input", Any: []string{"Do you want"}, Not: []string{"(auto-approved)"}}
	if !checkRule(rl, "Do you want to proceed?", nil) {
		t.Fatal("the any predicate did not match on its own")
	}
	if checkRule(rl, "Do you want to proceed? (auto-approved)", nil) {
		t.Fatal("the not predicate failed to veto")
	}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, errs := Load("")
	for _, err := range errs {
		t.Fatalf("the bundled manifests must parse: %v", err)
	}
	if r == nil {
		t.Fatal("no registry")
	}
	return r
}

// The three prompts Claude Code actually blocks on, written as they render.
// This is the fixture most likely to be wrong, so it is spelled out in full
// rather than reduced to the substring the rule happens to key on.
func TestClassifyCoversEveryClaudeBlocker(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct {
		name string
		tail []string
	}{
		{"tool permission", []string{
			"Bash(go test ./...)",
			"Do you want to proceed?",
			"❯ 1. Yes",
			"  2. Yes, and don't ask again for similar commands",
			"  3. No, and tell Claude what to do differently (esc)",
		}},
		{"edit permission", []string{
			"Do you want to make this edit to main.go?",
			"❯ 1. Yes",
			"  2. No, and tell Claude what to do differently (esc)",
		}},
		{"folder trust", []string{
			"Do you trust the files in this folder?",
			"/home/gaurav/dev/tuios",
		}},
		{"plan approval", []string{
			"Would you like to proceed?",
			"❯ 1. Yes, and auto-accept edits",
			"  2. Yes, and manually approve edits",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, _, ok := r.Classify("claude-code", tc.tail)
			if !ok {
				t.Fatalf("no rule matched a real blocking prompt:\n%s", strings.Join(tc.tail, "\n"))
			}
			if state != "needs_input" {
				t.Errorf("classified as %q, want needs_input", state)
			}
		})
	}
}
