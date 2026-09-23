package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestScreenExplanationNamesTheRuleAndTheReason is the whole point of the
// command. A person writing a screen rule needs two things the daemon otherwise
// keeps to itself: the text that was matched, and, for a rule that refused,
// which of its strings was the reason. Printing only "no match" would leave
// them exactly where they started.
func TestScreenExplanationNamesTheRuleAndTheReason(t *testing.T) {
	var b strings.Builder
	printScreenExplanation(&b, screenExplanation{
		WindowID:  "w-1",
		HarnessID: "claude-code",
		State:     "needs_input",
		Source:    "screen",
		Enabled:   true,
		Lines:     8,
		Tail:      []string{"Do you want to proceed?", "> 1. Yes"},
		Matched:   true,
		Rule:      0,
		RuleState: "needs_input",
		Rules: []harness.RuleReport{
			{Index: 0, State: "needs_input", Priority: 30, Matched: true},
			{Index: 1, State: "needs_input", Priority: 20, Missing: []string{"Do you trust"}},
			{Index: 2, State: "idle", Priority: 10, NoneOf: []string{"esc to interrupt"}},
			{Index: 3, State: "working", Priority: 5, Blocked: []string{"(auto-approved)"}},
		},
	})
	got := b.String()

	for _, want := range []string{
		"Do you want to proceed?", // the tail is dumped
		"claude-code",             // and attributed
		"reading 8 lines",         // with the window the rules see
		"rule 0 would report needs_input",
		`"Do you trust" is not on the screen`,
		`none of "esc to interrupt" is on the screen`,
		`"(auto-approved)" is on the screen and vetoes the rule`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not say %q:\n%s", want, got)
		}
	}
}

// TestScreenExplanationForAPaneWithNoHarness points at the fix rather than
// reporting an absence. Most panes are not agents, and the first rule for a new
// harness is always written against a pane nothing has claimed.
func TestScreenExplanationForAPaneWithNoHarness(t *testing.T) {
	var b strings.Builder
	printScreenExplanation(&b, screenExplanation{
		WindowID: "w-1",
		State:    "none",
		Source:   "report",
		Lines:    6,
		Tail:     []string{"$ ls"},
	})
	got := b.String()
	if !strings.Contains(got, "$ ls") {
		t.Errorf("the tail is dumped even with no harness:\n%s", got)
	}
	if !strings.Contains(got, "--harness") {
		t.Errorf("output does not name the flag that would run some rules:\n%s", got)
	}
}

// TestScreenExplanationSaysWhenNothingMatched keeps the silent answer explicit.
// No rule matching is the normal case and means the screen tier says nothing,
// which is different from the tier being off.
func TestScreenExplanationSaysWhenNothingMatched(t *testing.T) {
	var b strings.Builder
	printScreenExplanation(&b, screenExplanation{
		WindowID:  "w-1",
		HarnessID: "claude-code",
		Enabled:   true,
		Lines:     8,
		Tail:      []string{"ok"},
		Rule:      -1,
		Rules:     []harness.RuleReport{{Index: 0, State: "needs_input", Missing: []string{"Do you want"}}},
	})
	if got := b.String(); !strings.Contains(got, "report nothing") {
		t.Errorf("output does not say the tier reports nothing:\n%s", got)
	}
}

// TestScreenExplanationShowsRegionsGroupsTitlesAndTheFileInForce covers what
// the manifest engine added: a narrowed rule's region and the text it read, a
// nested group that refused, the title block with the progress report, and a
// user file that replaced the bundled manifest.
func TestScreenExplanationShowsRegionsGroupsTitlesAndTheFileInForce(t *testing.T) {
	var b strings.Builder
	printScreenExplanation(&b, screenExplanation{
		WindowID:        "w-1",
		HarnessID:       "claude-code",
		Enabled:         true,
		Lines:           8,
		Tail:            []string{"──────", "Esc to cancel"},
		Rule:            -1,
		ManifestSource:  "/home/u/.config/tuios/harnesses/claude-code.toml",
		ReplacesBundled: true,
		Rules: []harness.RuleReport{
			{Index: 0, State: "needs_input", Region: "after_last_horizontal_rule", Text: "Esc to cancel", Groups: []string{"no any_of group matched"}},
			{Index: 1, State: "idle", Region: "prompt_box", NoRegion: true},
		},
		Title:          "✳ Claude Code",
		Progress:       "4;0",
		TitleMatched:   true,
		TitleRule:      0,
		TitleRuleState: "idle",
		TitleRules:     []harness.RuleReport{{Index: 0, State: "idle", Matched: true}},
	})
	got := b.String()
	for _, want := range []string{
		"replaces the bundled one",
		"region after_last_horizontal_rule",
		"| Esc to cancel",
		"no any_of group matched",
		"region prompt_box is not on the screen",
		`title "✳ Claude Code"`,
		"progress 4;0",
		"title rule 0 would report idle",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not say %q:\n%s", want, got)
		}
	}
}
