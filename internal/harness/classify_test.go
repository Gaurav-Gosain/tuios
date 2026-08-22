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

// A rule naming no positive predicate would match every screen the harness ever
// paints, which is a rule that says the pane is always blocked.
func TestRuleWithNoPredicatesMatchesNothing(t *testing.T) {
	rl := &ScreenRule{State: "needs_input"}
	if checkRule(rl, "anything at all", "anything at all", nil) {
		t.Fatal("a rule with no predicates matched; it would claim every pane")
	}
	// Not alone is a veto with nothing to veto for, not a positive claim.
	rl = &ScreenRule{State: "needs_input", Not: []string{"quiet"}}
	if checkRule(rl, "anything at all", "anything at all", nil) {
		t.Fatal("a rule with only a veto matched; it would claim every pane not naming its veto")
	}
}

func TestNotPredicateVetoesAMatch(t *testing.T) {
	rl := &ScreenRule{State: "needs_input", Any: []string{"Do you want"}, Not: []string{"(auto-approved)"}}
	if !checkRule(rl, "Do you want to proceed?", "Do you want to proceed?", nil) {
		t.Fatal("the any predicate did not match on its own")
	}
	if checkRule(rl, "Do you want to proceed? (auto-approved)", "Do you want to proceed? (auto-approved)", nil) {
		t.Fatal("the not predicate failed to veto")
	}
}

// mustRule builds a compiled rule the way parseManifest would, so regex tests
// exercise the same compile path a manifest goes through.
func mustRule(t *testing.T, rl ScreenRule, foldCase bool) *ScreenRule {
	t.Helper()
	if err := rl.compile(foldCase); err != nil {
		t.Fatalf("rule failed to compile: %v", err)
	}
	return &rl
}

func TestRegexPredicateAnchorsLines(t *testing.T) {
	rl := mustRule(t, ScreenRule{State: "working", Regex: []string{`^\s*⠋ Thinking`}}, false)
	hay := "some earlier output\n  ⠋ Thinking hard\n"
	if !checkRule(rl, hay, hay, nil) {
		t.Fatal("a line-anchored pattern did not match its line; ^ must mean line start in a joined tail")
	}
	hay = "prefix ⠋ Thinking inline"
	if checkRule(rl, hay, hay, nil) {
		t.Fatal("^ matched mid-line; the anchor would be meaningless on a joined tail")
	}
}

func TestRegexAloneIsAPositivePredicate(t *testing.T) {
	rl := mustRule(t, ScreenRule{State: "working", Regex: []string{`\[stop\]`}}, false)
	if !checkRule(rl, "⠧ Waiting 2.8s [stop]", "⠧ waiting 2.8s [stop]", nil) {
		t.Fatal("a regex-only rule did not match")
	}
	if checkRule(rl, "all done", "all done", nil) {
		t.Fatal("a regex-only rule matched a screen its pattern is absent from")
	}
}

func TestNotRegexVetoesAMatch(t *testing.T) {
	rl := mustRule(t, ScreenRule{
		State:    "idle",
		Regex:    []string{`^❯ `},
		NotRegex: []string{`^( [\x{2800}-\x{28FF}]){1,2} `},
	}, false)
	if !checkRule(rl, "❯ ready", "❯ ready", nil) {
		t.Fatal("the positive regex did not match on its own")
	}
	hay := "❯ ready\n ⠧ [BUILD]"
	if checkRule(rl, hay, hay, nil) {
		t.Fatal("not_regex failed to veto")
	}
}

// Case folding is per manifest and applies to substrings only: a regex says
// (?i) when it wants folding, and the screen keeps its case for patterns.
func TestFoldCaseFoldsSubstringsNotRegex(t *testing.T) {
	data := []byte(`
schema_version = 1
id = "folded"
[detect]
comm = ["folded"]
[screen]
enabled = true
fold_case = true
[[screen.rule]]
state = "needs_input"
priority = 10
all = ["Approve ONCE"]
[[screen.rule]]
state = "working"
priority = 5
regex = ['^RUNNING']
`)
	m, err := parseManifest("folded.toml", data)
	if err != nil {
		t.Fatal(err)
	}
	r := &Registry{manifests: []*Manifest{m}}
	state, _, ok := r.Classify("folded", []string{"aPPROVE oNCE"})
	if !ok || state != "needs_input" {
		t.Fatalf("folded substring did not match regardless of case: state=%q ok=%v", state, ok)
	}
	if _, _, ok := r.Classify("folded", []string{"running build"}); ok {
		t.Fatal("an unfolded regex matched lowercased text; fold_case must not rewrite patterns")
	}
	state, _, ok = r.Classify("folded", []string{"RUNNING build"})
	if !ok || state != "working" {
		t.Fatalf("case-exact regex did not match: state=%q ok=%v", state, ok)
	}
}

func TestBadRegexRefusesTheManifestByName(t *testing.T) {
	data := []byte(`
schema_version = 1
id = "broken"
[detect]
comm = ["broken"]
[screen]
enabled = true
[[screen.rule]]
state = "working"
regex = ['(unclosed']
`)
	if _, err := parseManifest("broken.toml", data); err == nil {
		t.Fatal("a manifest with an uncompilable pattern loaded; it would have shipped a rule that can never run")
	}
}

func TestOversizedPatternIsRefused(t *testing.T) {
	rl := ScreenRule{State: "working", Regex: []string{strings.Repeat("a", maxScreenPattern+1)}}
	if err := rl.compile(false); err == nil {
		t.Fatal("a pattern past the size cap compiled; the cap exists so the screen scan's cost stays readable")
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
