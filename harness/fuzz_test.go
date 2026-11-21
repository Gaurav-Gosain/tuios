package harness

import (
	"maps"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

func fuzzTail(s string) []string {
	if len(s) > 1<<14 {
		s = s[:1<<14]
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxRegionLines {
		lines = lines[len(lines)-maxRegionLines:]
	}
	return lines
}

func agreeOn(t *testing.T, r *Registry, id string, tail []string) {
	t.Helper()
	m := r.Lookup(id)
	state, rule, ok := r.Classify(id, tail)
	estate, erule, reports := r.Explain(id, tail)

	if ok {
		if rule < 0 || rule >= len(m.Screen.Rule) {
			t.Fatalf("%s: Classify matched rule %d of %d", id, rule, len(m.Screen.Rule))
		}
		if _, known := screenStates[state]; !known {
			t.Fatalf("%s: Classify returned state %q, which a screen rule may not assert", id, state)
		}
		if m.Screen.Rule[rule].State != state {
			t.Fatalf("%s: Classify returned state %q for rule %d, which asserts %q",
				id, state, rule, m.Screen.Rule[rule].State)
		}
		_ = r.RuleMessage(id, rule)
	} else if state != "" || rule != -1 {
		t.Fatalf("%s: Classify missed but returned state %q rule %d", id, state, rule)
	}

	if !ok {
		if erule != -1 {
			t.Fatalf("%s: Classify found no match, Explain picked rule %d (%s)\ntail: %q",
				id, erule, estate, tail)
		}
	} else if erule != rule || estate != state {
		t.Fatalf("%s: Classify picked rule %d (%s), Explain picked rule %d (%s)\ntail: %q",
			id, rule, state, erule, estate, tail)
	}

	if m != nil && len(reports) != len(m.Screen.Rule) {
		t.Fatalf("%s: Explain reported %d rules of %d", id, len(reports), len(m.Screen.Rule))
	}
	for _, rep := range reports {
		if rep.Text != "" && !utf8.ValidString(rep.Text) {
			valid := true
			for _, l := range tail {
				valid = valid && utf8.ValidString(l)
			}
			if valid {
				t.Fatalf("%s: rule %d report text is cut inside a character: %q", id, rep.Index, rep.Text)
			}
		}
	}
}

var screenSeeds = []string{
	"",
	"\n\n",
	"Do you want to proceed?\n❯ 1. Yes\n  2. No",
	"╭──────────────╮\n│ >            │\n╰──────────────╯",
	"╭──────────────╮\n│ > half a box",
	"esc to interrupt",
	"Allow this command? (y/n)",
	"agent is requesting approval to run command:\n→ [1] Approve once\n  [2] Approve for this session\n  [3] Reject\n  ▲/▼ select  1/2/3/4 choose  ↵ confirm",
	"│ Allow execution of: 'go'?            │\n│ ● Allow once                          │\n│   No, suggest changes (esc)           │",
	"\xff\xfe\n\xed\xa0\x80",
	strings.Repeat("é", 3000),
	strings.Repeat("line\n", 300),
}

// FuzzClassifyAgreesWithExplain runs every bundled harness's rules over an
// arbitrary tail and requires the classifier and the explanation to agree.
func FuzzClassifyAgreesWithExplain(f *testing.F) {
	r, errs := Load()
	if len(errs) > 0 {
		f.Fatalf("loading the bundled manifests: %v", errs)
	}
	for _, s := range screenSeeds {
		f.Add(s)
	}
	for _, id := range r.IDs() {
		for _, rl := range r.Lookup(id).Screen.Rule {
			f.Add(strings.Join(append(append([]string{}, rl.All...), rl.Any...), "\n"))
		}
	}

	f.Fuzz(func(t *testing.T, screen string) {
		tail := fuzzTail(screen)
		for _, id := range r.IDs() {
			agreeOn(t, r, id, tail)
		}
	})
}

var fuzzRegions = []string{"", "tail", "prompt_box", "above_prompt_box",
	"last_non_empty_above_prompt_box", "after_last_horizontal_rule",
	"bottom_non_empty_lines(1)", "bottom_non_empty_lines(3)", "bottom_non_empty_lines(0)"}

// FuzzManifestRules builds a manifest from fuzzed rule parts rather than from
// TOML text. FuzzManifest mutates bytes, and almost every mutation of a
// manifest is a TOML error or a manifest that fails its own checks, so it
// rarely gets a rule to the classifier. Here every input is a well-formed file
// and the mutator spends its budget on priorities, strings, patterns and
// regions, which is where the two paths could part.
func FuzzManifestRules(f *testing.F) {
	f.Add(int64(9223372036854775807), int64(-2), uint8(1), uint8(0), uint8(0), uint8(2), false,
		"a", "", "", "b", "a b")
	f.Add(int64(0), int64(0), uint8(0), uint8(0), uint8(2), uint8(2), true,
		"Esc", "x", "^> ", "ESC", "│ > \n╰─╯\nesc to interrupt")
	f.Add(int64(-5), int64(5), uint8(2), uint8(1), uint8(6), uint8(3), false,
		"é", "É", "(a|b)+$", "", "É\né")

	states := []string{"working", "needs_input", "idle"}
	f.Fuzz(func(t *testing.T, pri1, pri2 int64, st1, st2, reg1, reg2 uint8, fold bool,
		all1, not1, re1, any2, screen string) {
		rule := func(state string, pri int64, region string, gate map[string]any) map[string]any {
			r := map[string]any{"state": state, "priority": pri, "region": region}
			maps.Copy(r, gate)
			return r
		}
		list := func(s string) []string {
			if s == "" {
				return nil
			}
			return strings.Split(s, "\n")
		}
		doc := map[string]any{
			"schema_version": SchemaVersion,
			"id":             "fuzzagent",
			"detect":         map[string]any{"comm": []string{"fuzzagent"}},
			"screen": map[string]any{
				"enabled":   true,
				"fold_case": fold,
				"rule": []map[string]any{
					rule(states[int(st1)%len(states)], pri1, fuzzRegions[int(reg1)%len(fuzzRegions)],
						map[string]any{"all": list(all1), "not": list(not1), "regex": list(re1)}),
					rule(states[int(st2)%len(states)], pri2, fuzzRegions[int(reg2)%len(fuzzRegions)],
						map[string]any{"any": list(any2)}),
				},
			},
		}
		data, err := toml.Marshal(doc)
		if err != nil {
			return
		}
		m, err := parseManifest("fuzz.toml", data)
		if err != nil {
			return
		}
		r := &Registry{manifests: []*Manifest{m}}
		agreeOn(t, r, m.ID, fuzzTail(screen))
	})
}

// FuzzManifest parses an arbitrary manifest the way the daemon loads one from
// the user's directory, and when it is accepted, uses it: the same agreement
// check over an arbitrary tail. A manifest is hand-edited by users, so what
// parses has to be safe to run.
func FuzzManifest(f *testing.F) {
	files, err := bundledFiles()
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range files {
		f.Add(file.data, "esc to interrupt\nDo you want to proceed?")
	}
	f.Add([]byte("schema_version = 1\nid = \"x\"\n[detect]\ncomm = [\"xagent\"]\n"+
		"[screen]\nenabled = true\n"+
		"[[screen.rule]]\nstate = \"needs_input\"\npriority = 9223372036854775807\nall = [\"a\"]\n"+
		"[[screen.rule]]\nstate = \"working\"\npriority = -2\nall = [\"a\"]\n"), "a")

	f.Fuzz(func(t *testing.T, data []byte, screen string) {
		if len(data) > 1<<16 {
			return
		}
		m, err := parseManifest("fuzz.toml", data)
		if err != nil {
			return
		}
		if len(m.Screen.order) != len(m.Screen.Rule) {
			t.Fatalf("parsed manifest has %d rules and a scan order of %d", len(m.Screen.Rule), len(m.Screen.order))
		}
		r := &Registry{manifests: []*Manifest{m}}
		agreeOn(t, r, m.ID, fuzzTail(screen))
		_ = r.ScreenLines(m.ID)
	})
}
