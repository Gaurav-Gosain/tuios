package harness

import "testing"

// titleCase is one window title and what a harness's title rules must make of
// it: a state, or "none".
type titleCase struct {
	harness, title, want string
}

// titleCases are the titles each bundled title rule is held to. The sources
// are in each manifest's [title] comment: measured for claude-code, Gemini
// CLI's windowTitle.ts for gemini-cli, and herdr's manifests for the others.
var titleCases = []titleCase{
	{"claude-code", "✳ Claude Code", "idle"},
	{"claude-code", "⠂ Fix the flaky test", "working"},
	{"claude-code", "Claude Code", "none"},

	{"codex", "Action Required - codex", "needs_input"},
	{"codex", "⠋ codex", "working"},
	{"codex", "codex", "none"},

	// Gemini CLI pads its title to 80 columns and appends the folder.
	{"gemini-cli", "✋  Action Required (tuios)                                                      ", "needs_input"},
	{"gemini-cli", "⏲  Working… (tuios)", "working"},
	{"gemini-cli", "✦  Refactoring the retry loop (tuios)", "working"},
	{"gemini-cli", "✦  Action required: read the migration notes", "working"},
	{"gemini-cli", "✦  Ready to write the tests (tuios)", "working"},
	{"gemini-cli", "◇  Ready (tuios)", "idle"},
	{"gemini-cli", "Gemini CLI (tuios)", "none"},
	{"gemini-cli", "~/src/ready-player-one", "none"},

	{"amp", "Plugin confirmation needed - amp - ~/src/app", "needs_input"},
	{"amp", "⠹ fix the flaky test", "working"},
	{"amp", "fix the flaky test - amp - ~/src/app", "idle"},
	{"amp", "~/src/amp-notes", "none"},

	{"grok", "⚠ Action Required - review - grok", "needs_input"},
	{"grok", "review ⠧ Running tests", "working"},
	{"grok", "review - grok", "idle"},
	{"grok", "grok", "idle"},
	{"grok", "~/src/grokking", "none"},

	{"hermes", "⚠️ hermes: approve rm -rf build", "needs_input"},
	{"hermes", "⏳ hermes: reading files", "working"},
	{"hermes", "✓ hermes", "idle"},
	{"hermes", "hermes", "none"},

	{"kiro", "◐ kiro: fixing the build", "working"},
	{"kiro", "kiro", "none"},

	{"qwen", "✳ Waiting for confirmation", "needs_input"},
	{"qwen", "◐ Reading files", "working"},
	{"qwen", "Qwen Code", "none"},
}

// TestBundledTitleRules holds every bundled title rule to titleCases, and
// every title rule to at least one case it decides.
func TestBundledTitleRules(t *testing.T) {
	r := testRegistry(t)
	decided := map[string]map[int]bool{}
	for _, tc := range titleCases {
		state, rule, ok := r.ClassifyTitle(tc.harness, tc.title)
		got := "none"
		if ok {
			got = state
		}
		if got != tc.want {
			t.Errorf("%s title %q: got %s, want %s", tc.harness, tc.title, got, tc.want)
			continue
		}
		if ok {
			if decided[tc.harness] == nil {
				decided[tc.harness] = map[int]bool{}
			}
			decided[tc.harness][rule] = true
		}
	}
	for _, id := range r.IDs() {
		m := r.Lookup(id)
		if !m.Title.Enabled {
			continue
		}
		for i, rule := range m.Title.Rule {
			if !decided[id][i] {
				t.Errorf("%s title rule %d (%s) decides no case in titleCases", id, i, rule.State)
			}
		}
	}
}
