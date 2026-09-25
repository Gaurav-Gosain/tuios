package harness

import (
	"strings"
	"testing"
)

// manifestHead is the part of a test manifest every case shares.
const manifestHead = "schema_version = 1\nid = \"x-agent\"\n[detect]\ncomm = [\"x-agent\"]\n"

func mustManifest(t *testing.T, body string) *Registry {
	t.Helper()
	m, err := parseManifest("x.toml", []byte(manifestHead+body))
	if err != nil {
		t.Fatalf("manifest refused: %v", err)
	}
	return &Registry{manifests: []*Manifest{m}}
}

func classified(r *Registry, tail ...string) string {
	state, _, ok := r.Classify("x-agent", tail)
	if !ok {
		return "none"
	}
	return state
}

// TestNestedGates holds the three nested lists to their meaning: every all_of
// group, at least one any_of group, and no none_of group.
func TestNestedGates(t *testing.T) {
	r := mustManifest(t, `
[screen]
enabled   = true
fold_case = true
lines     = 8

[[screen.rule]]
state = "needs_input"
all   = ["do you want to proceed?"]
all_of = [
  { any_of = [ { regex = ['(?i)^\s*❯?\s*1\.\s*yes\b'] }, { regex = ['(?i)^\s*2\.\s*no\b'] } ] },
]
any_of = [ { all = ["bash command"] }, { any = ["tab to amend", "ctrl+e to explain"] } ]
none_of = [ { all = ["auto-approved", "yes"] } ]
`)
	for _, tc := range []struct {
		name string
		tail []string
		want string
	}{
		{"every group met", []string{"Bash command", "Do you want to proceed?", "❯ 1. Yes"}, "needs_input"},
		{"second any_of branch", []string{"Do you want to proceed?", "  2. No", "Tab to amend"}, "needs_input"},
		{"no option line fails all_of", []string{"Bash command", "Do you want to proceed?"}, "none"},
		{"no any_of branch", []string{"Do you want to proceed?", "❯ 1. Yes"}, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classified(r, tc.tail...); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
	// The none_of group is "auto-approved" and "yes" together, so it vetoes
	// only when both are on the screen.
	if got := classified(r, "Bash command", "Do you want to proceed?", "  2. No", "auto-approved"); got != "needs_input" {
		t.Errorf("a partial none_of group vetoed: got %s", got)
	}
	if got := classified(r, "Bash command", "Do you want to proceed?", "❯ 1. Yes", "auto-approved yes"); got != "none" {
		t.Errorf("a whole none_of group did not veto: got %s", got)
	}
}

// TestNestedGateLoadErrors: a group that names nothing present, a veto group
// that names nothing at all, and a tree past the depth limit are refused at
// load, where the error names the file.
func TestNestedGateLoadErrors(t *testing.T) {
	deep := "all_of = [ " + strings.Repeat("{ all = [\"a\"], all_of = [ ", maxGateDepth+1) + "{ all = [\"z\"] }" + strings.Repeat(" ] }", maxGateDepth+1) + " ]\n"
	for _, tc := range []struct {
		name, rule string
	}{
		{"empty all_of group", "all = [\"x\"]\nall_of = [ { not = [\"y\"] } ]\n"},
		{"empty any_of group", "all = [\"x\"]\nany_of = [ { } ]\n"},
		{"empty none_of group", "all = [\"x\"]\nnone_of = [ { } ]\n"},
		{"bad nested pattern", "all = [\"x\"]\nall_of = [ { regex = ['(open'] } ]\n"},
		{"too deep", "all = [\"x\"]\n" + deep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"working\"\n" + tc.rule
			if _, err := parseManifest("x.toml", []byte(manifestHead+body)); err == nil {
				t.Error("loaded")
			}
		})
	}
	// A veto group of vetoes is allowed: "none of: a screen without X".
	body := "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"working\"\nall = [\"x\"]\nnone_of = [ { not = [\"y\"] } ]\n"
	if _, err := parseManifest("x.toml", []byte(manifestHead+body)); err != nil {
		t.Errorf("a veto group of vetoes was refused: %v", err)
	}
}

// TestPredicateBudget: a manifest cannot carry an unbounded number of
// predicates, since every one of them runs on every settle.
func TestPredicateBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("[screen]\nenabled = true\n")
	for i := 0; i < maxPredicates/10+1; i++ {
		b.WriteString("[[screen.rule]]\nstate = \"working\"\nany = [")
		for j := range 11 {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("\"s\"")
		}
		b.WriteString("]\n")
	}
	if _, err := parseManifest("x.toml", []byte(manifestHead+b.String())); err == nil {
		t.Error("a manifest past the predicate budget loaded")
	}
}

// TestScreenRegions checks each region on its own against one screen.
func TestScreenRegions(t *testing.T) {
	tail := []string{
		"old answer: Do you want to proceed?",
		"────────────",
		"✻ Waiting for 2 background agents to finish",
		"────────────",
		"❯ ",
		"────────────",
		"  ⏵⏵ accept edits on",
	}
	for _, tc := range []struct {
		region string
		want   []string
	}{
		{"", tail},
		{"tail", tail},
		{"whole_recent", tail},
		{"bottom_non_empty_lines(2)", tail[5:]},
		{"bottom_non_empty_lines(99)", tail},
		{"prompt_box", []string{"❯ "}},
		{"prompt_box_body", []string{"❯ "}},
		{"above_prompt_box", tail[:3]},
		{"last_non_empty_above_prompt_box", []string{"✻ Waiting for 2 background agents to finish"}},
		{"after_last_horizontal_rule", []string{"  ⏵⏵ accept edits on"}},
	} {
		t.Run(tc.region, func(t *testing.T) {
			region, _, err := normalizeScreenRegion(tc.region)
			if err != nil {
				t.Fatal(err)
			}
			got := regionLines(tail, region)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	// No rule on the screen: after_last_horizontal_rule reads everything, as
	// herdr's does, and the box regions read nothing.
	plain := []string{"a", "b"}
	if got := regionLines(plain, RegionAfterLastRule); len(got) != 2 {
		t.Errorf("after_last_horizontal_rule with no rule = %q, want the whole tail", got)
	}
	if got := regionLines(plain, RegionLastAbovePromptBox); got != nil {
		t.Errorf("last_non_empty_above_prompt_box with no box = %q, want nothing", got)
	}
}

// TestRegionNames: region names are checked at load and bottom regions have to
// fit inside the lines the manifest reads.
func TestRegionNames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		ok    bool
	}{
		{"bottom inside lines", "[screen]\nenabled = true\nlines = 8\n[[screen.rule]]\nstate = \"working\"\nregion = \"bottom_non_empty_lines(8)\"\nall = [\"x\"]\n", true},
		{"bottom past lines", "[screen]\nenabled = true\nlines = 8\n[[screen.rule]]\nstate = \"working\"\nregion = \"bottom_non_empty_lines(9)\"\nall = [\"x\"]\n", false},
		{"bottom zero", "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"working\"\nregion = \"bottom_non_empty_lines(0)\"\nall = [\"x\"]\n", false},
		{"bottom leading zero", "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"working\"\nregion = \"bottom_non_empty_lines(03)\"\nall = [\"x\"]\n", false},
		{"bottom not a number", "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"working\"\nregion = \"bottom_non_empty_lines(x)\"\nall = [\"x\"]\n", false},
		{"lines past the cap", "[screen]\nenabled = true\nlines = 500\n[[screen.rule]]\nstate = \"working\"\nall = [\"x\"]\n", false},
		{"title region on a screen rule", "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"working\"\nregion = \"osc_title\"\nall = [\"x\"]\n", false},
		{"osc_progress on a title rule", "[title]\nenabled = true\n[[title.rule]]\nstate = \"working\"\nregion = \"osc_progress\"\nregex = ['^4;3']\n", true},
		{"osc_title on a title rule", "[title]\nenabled = true\n[[title.rule]]\nstate = \"working\"\nregion = \"osc_title\"\nall = [\"x\"]\n", true},
		{"screen region on a title rule", "[title]\nenabled = true\n[[title.rule]]\nstate = \"working\"\nregion = \"prompt_box\"\nall = [\"x\"]\n", false},
		{"region on a notify rule", "[notify]\nenabled = true\n[[notify.rule]]\nstate = \"done\"\nregion = \"osc_title\"\nall = [\"x\"]\n", false},
		{"idle proven by a nested pattern", "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"idle\"\nall = [\"ready\"]\nall_of = [ { regex = ['^> $'] } ]\n", true},
		{"idle with a pattern on one any_of branch only", "[screen]\nenabled = true\n[[screen.rule]]\nstate = \"idle\"\nany_of = [ { regex = ['^> $'] }, { all = [\"ready\"] } ]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest("x.toml", []byte(manifestHead+tc.block))
			if (err == nil) != tc.ok {
				t.Errorf("load error %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestBottomRegionReadsTheBottom: a rule reading the last line does not see a
// footer word further up, which is the whole point of narrowing it.
func TestBottomRegionReadsTheBottom(t *testing.T) {
	r := mustManifest(t, `
[screen]
enabled = true
lines   = 6
[[screen.rule]]
state  = "working"
region = "bottom_non_empty_lines(1)"
regex  = ['^( [\x{2800}-\x{28FF}]){1,2} \[(BUILD|PLAN)\]']
`)
	if got := classified(r, "❯ ", " ⠧ [BUILD] model"); got != "working" {
		t.Errorf("status bar spinner: got %s", got)
	}
	if got := classified(r, " ⠧ [BUILD] model", "❯ "); got != "none" {
		t.Errorf("a spinner line above the last line matched a bottom(1) rule: got %s", got)
	}
}

// TestProgressRules: a title-block rule can read the last OSC 9;4 report, and
// a harness without such a rule reports none.
func TestProgressRules(t *testing.T) {
	r := mustManifest(t, `
[title]
enabled = true
[[title.rule]]
state    = "needs_input"
priority = 10
region   = "osc_progress"
regex    = ['^4;3(?:;|$)']
[[title.rule]]
state    = "working"
priority = 5
regex    = ['^⠋ ']
`)
	if !r.HasProgressRules("x-agent") {
		t.Fatal("HasProgressRules is false for a manifest with one")
	}
	if state, _, _ := r.ClassifyOSC("x-agent", "⠋ build", ProgressText(3, 0)); state != "needs_input" {
		t.Errorf("progress rule did not outrank the title rule: %s", state)
	}
	if state, _, _ := r.ClassifyOSC("x-agent", "⠋ build", ProgressText(1, 40)); state != "working" {
		t.Errorf("title rule did not answer when the progress rule missed: %s", state)
	}
	if _, _, ok := r.ClassifyTitle("x-agent", "plain"); ok {
		t.Error("ClassifyTitle matched with no progress and a plain title")
	}
	if state, _, _ := r.ClassifyOSC("x-agent", "", "4;3"); state != "needs_input" {
		t.Errorf("a progress report with no title did not classify: %s", state)
	}
	base := testRegistry(t)
	for _, id := range base.IDs() {
		if base.HasProgressRules(id) {
			t.Errorf("bundled %s reads osc_progress; the published meaning covers every bundled harness, so no bundled rule should", id)
		}
	}
	for _, tc := range []struct {
		state, percent int
		want           string
	}{{0, 0, "4;0"}, {1, 40, "4;1;40"}, {2, 0, "4;2;0"}, {3, 9, "4;3"}, {4, 70, "4;4;70"}} {
		if got := ProgressText(tc.state, tc.percent); got != tc.want {
			t.Errorf("ProgressText(%d, %d) = %q, want %q", tc.state, tc.percent, got, tc.want)
		}
	}
}
