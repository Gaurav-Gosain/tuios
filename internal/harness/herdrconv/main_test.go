package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// sample is shaped like herdr's own manifests: nested gates, a narrowed
// region, title and progress rules, and the rules tuios cannot carry.
const sample = `id = "sample"
version = "2026.09.11.1"

[[rules]]
id = "osc_title_working"
state = "working"
priority = 1100
region = "osc_title"
regex = ['^[\x{2800}-\x{28FF}] ']

[[rules]]
id = "osc_progress_blocked"
state = "blocked"
priority = 1050
region = "osc_progress"
regex = ['^4;3(?:;|$)']

[[rules]]
id = "live_blocked_form"
state = "blocked"
priority = 980
region = "after_last_horizontal_rule"
contains = ["esc to cancel"]
any = [
  { contains = ["enter to confirm"] },
  { contains = ["enter to select"], any = [
    { contains = ["↑/↓ to navigate"] },
    { contains = ["arrows to navigate"] },
  ] },
]
not = [
  { contains = ["auto-approved"] },
  { regex = ['(?m)^\s*❯\s*$'] },
  { contains = ["a", "b"] },
]

[[rules]]
id = "status_working"
state = "working"
priority = 900
region = "bottom_non_empty_lines(3)"
line_regex = ['^\s*[\u2800-\u28FF]+\s+\p{Alphabetic}+ing\b']

[[rules]]
id = "viewer"
state = "unknown"
priority = 1000
contains = ["showing transcript"]

[[rules]]
id = "top"
state = "working"
priority = 10
region = "top_non_empty_lines(1)"
contains = ["busy"]
`

// TestConvertCarriesNestedGatesRegionsAndTitles converts the sample, loads the
// draft through the real loader, and classifies screens and titles with it, so
// the conversion is held to what tuios does with the result rather than to
// its text.
func TestConvertCarriesNestedGatesRegionsAndTitles(t *testing.T) {
	res, err := convert([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if res.kept != 4 || res.dropped != 2 || res.screen != 2 || res.titles != 2 {
		t.Fatalf("kept %d dropped %d screen %d title %d, want 4 2 2 2\n%s", res.kept, res.dropped, res.screen, res.titles, strings.Join(res.report, "\n"))
	}
	report := strings.Join(res.report, "\n")
	for _, want := range []string{`dropped viewer (unknown, priority 1000): state "unknown"`, `dropped top (working, priority 10): region "top_non_empty_lines(1)"`} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}

	draft := strings.ReplaceAll(res.draft, "enabled   = false", "enabled   = true")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.toml"), []byte(draft), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, errs := harness.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("the draft does not load: %v\n%s", errs, draft)
	}

	for _, tc := range []struct {
		name string
		tail []string
		want string
	}{
		{"select form under the rule", []string{"Do it?", "──────", "Enter to select · ↑/↓ to navigate · Esc to cancel"}, "needs_input"},
		{"select without a navigate hint", []string{"──────", "Enter to select · Esc to cancel"}, "none"},
		{"confirm form vetoed", []string{"──────", "Enter to confirm · Esc to cancel · auto-approved"}, "none"},
		{"the multi-string not group vetoes only whole", []string{"──────", "Enter to confirm · Esc to cancel · a"}, "needs_input"},
		{"spinner in the bottom lines", []string{"⠹ Reading files"}, "working"},
	} {
		got := "none"
		if state, _, ok := reg.Classify("sample", tc.tail); ok {
			got = state
		}
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
	if state, _, _ := reg.ClassifyTitle("sample", "⠹ build"); state != "working" {
		t.Errorf("title rule: got %q", state)
	}
	if state, _, _ := reg.ClassifyOSC("sample", "", "4;3"); state != "needs_input" {
		t.Errorf("progress rule: got %q", state)
	}
}
