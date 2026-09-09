package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// parseAgentRowTOML runs a config file through the real decoder and the
// reader, the way a person's config.toml reaches the rail.
func parseAgentRowTOML(t *testing.T, body string) SidebarAgentRowSpec {
	t.Helper()
	cfg, err := ParseUserConfig([]byte(body))
	if err != nil {
		t.Fatalf("ParseUserConfig: %v", err)
	}
	return ParseSidebarAgentRow(cfg.Appearance.Sidebar.AgentRow)
}

// TestAgentRowDefaultIsTheShippedRow: no table means the row as it ships.
func TestAgentRowDefaultIsTheShippedRow(t *testing.T) {
	spec := parseAgentRowTOML(t, "[appearance.sidebar]\nenabled = true\n")
	if !slices.Equal(spec.Tokens, SidebarAgentRowDefaultTokens) {
		t.Fatalf("tokens = %v, want the shipped %v", spec.Tokens, SidebarAgentRowDefaultTokens)
	}
	if spec.Custom() || len(spec.Problems) != 0 {
		t.Fatalf("an absent table is custom (%v) or has problems (%v)", spec.Custom(), spec.Problems)
	}
}

// TestAgentRowReadsTokensLooksAndRules is the surface as documented: an
// ordered token list, a look per token, ordered rules with one test each.
func TestAgentRowReadsTokensLooksAndRules(t *testing.T) {
	spec := parseAgentRowTOML(t, `
[appearance.sidebar.agent_row]
tokens = ["name", "state", "elapsed"]

[appearance.sidebar.agent_row.name]
fg = "text"
bold = false

[[appearance.sidebar.agent_row.name.rule]]
contains = "test"
ignore_case = true
fg = "info"

[[appearance.sidebar.agent_row.elapsed.rule]]
gt = 30
fg = "warning"
bold = true

[[appearance.sidebar.agent_row.elapsed.rule]]
lt = 1
fg = "#00ff00"
`)
	if len(spec.Problems) != 0 {
		t.Fatalf("problems: %v", spec.Problems)
	}
	if want := []string{"name", "state", "elapsed"}; !slices.Equal(spec.Tokens, want) {
		t.Fatalf("tokens = %v, want %v", spec.Tokens, want)
	}
	name := spec.Style("name")
	if name.Base.Fg != "text" || name.Base.Bold == nil || *name.Base.Bold {
		t.Fatalf("name base = %+v, want fg text and bold false", name.Base)
	}
	if look := name.Resolve("Unit TESTS", 0, false); look.Fg != "info" {
		t.Fatalf("a case-folded contains rule did not match: %+v", look)
	}
	if look := name.Resolve("build", 0, false); look.Fg != "text" {
		t.Fatalf("a non-matching rule changed the look: %+v", look)
	}
	elapsed := spec.Style("elapsed")
	if len(elapsed.Rules) != 2 {
		t.Fatalf("elapsed has %d rules, want 2", len(elapsed.Rules))
	}
	if look := elapsed.Resolve("45m", 45, true); look.Fg != "warning" || look.Bold == nil || !*look.Bold {
		t.Fatalf("gt 30 did not match 45 minutes: %+v", look)
	}
	if look := elapsed.Resolve("<1m", 0.5, true); look.Fg != "#00ff00" {
		t.Fatalf("lt 1 did not match half a minute: %+v", look)
	}
	if look := elapsed.Resolve("", 0, false); look.Fg != "" {
		t.Fatalf("a token with no number matched a numeric rule: %+v", look)
	}
	if !spec.Custom() || spec.RuleCount() != 3 {
		t.Fatalf("Custom = %v, RuleCount = %d", spec.Custom(), spec.RuleCount())
	}
}

// TestAgentRowFailsSafely: every value the reader does not understand is
// dropped and named, and the config still loads with the rest intact.
func TestAgentRowFailsSafely(t *testing.T) {
	spec := parseAgentRowTOML(t, `
[appearance]
border_style = "double"

[appearance.sidebar.agent_row]
tokens = ["name", "colour", "name"]

[appearance.sidebar.agent_row.name]
fg = "purple"
bold = "yes"
size = 3

[[appearance.sidebar.agent_row.name.rule]]
equals = "a"
contains = "b"
fg = "info"

[[appearance.sidebar.agent_row.name.rule]]
fg = "info"

[[appearance.sidebar.agent_row.name.rule]]
gt = "thirty"
fg = "info"

[[appearance.sidebar.agent_row.name.rule]]
equals = "keep"
fg = "success"

[appearance.sidebar.agent_row.planet]
fg = "info"
`)
	if want := []string{"name"}; !slices.Equal(spec.Tokens, want) {
		t.Fatalf("tokens = %v, want %v", spec.Tokens, want)
	}
	name := spec.Style("name")
	if name.Base.Fg != "" || name.Base.Bold != nil {
		t.Fatalf("a bad fg or bold was kept: %+v", name.Base)
	}
	if len(name.Rules) != 1 || name.Rules[0].Text != "keep" {
		t.Fatalf("rules = %+v, want the one good rule", name.Rules)
	}
	for _, want := range []string{
		"no agent row token called \"colour\"",
		"listed twice",
		"fg is \"purple\"",
		"bold must be true or false",
		"size is not a key",
		"has equals and contains",
		"has no test",
		"gt must be a number",
		"no agent row token called \"planet\"",
	} {
		found := false
		for _, p := range spec.Problems {
			if strings.Contains(p, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no problem mentions %q; problems were:\n  %s", want, strings.Join(spec.Problems, "\n  "))
		}
	}

	// The validator prints them under the table's name.
	cfg, _ := ParseUserConfig([]byte("[appearance.sidebar.agent_row]\ntokens = [\"planet\"]\n"))
	result := ValidateConfig(cfg)
	found := false
	for _, w := range result.Warnings {
		if w.Field == "appearance.sidebar.agent_row" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the validator did not warn about the table: %+v", result.Warnings)
	}
}

// TestAgentRowCapsRulesAtEight: the ninth rule and after are dropped with a
// warning.
func TestAgentRowCapsRulesAtEight(t *testing.T) {
	var b strings.Builder
	b.WriteString("[appearance.sidebar.agent_row]\n")
	for range 10 {
		b.WriteString("[[appearance.sidebar.agent_row.name.rule]]\nequals = \"x\"\nfg = \"info\"\n")
	}
	spec := parseAgentRowTOML(t, b.String())
	if n := len(spec.Style("name").Rules); n != SidebarAgentRowMaxRules {
		t.Fatalf("kept %d rules, want %d", n, SidebarAgentRowMaxRules)
	}
	if len(spec.Problems) != 1 || !strings.Contains(spec.Problems[0], "more than 8 rules") {
		t.Fatalf("problems = %v", spec.Problems)
	}
}

// TestAgentRowSurvivesASave: the settings page writes the config back out, and
// the table has to come back through the writer as it went in.
func TestAgentRowSurvivesASave(t *testing.T) {
	body := `
[appearance.sidebar.agent_row]
tokens = ["harness", "name", "elapsed"]

[[appearance.sidebar.agent_row.elapsed.rule]]
gt = 30
fg = "warning"
`
	cfg, err := ParseUserConfig([]byte(body))
	if err != nil {
		t.Fatalf("ParseUserConfig: %v", err)
	}
	out, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	again, err := ParseUserConfig(out)
	if err != nil {
		t.Fatalf("ParseUserConfig(marshalled): %v\n%s", err, out)
	}
	before, after := ParseSidebarAgentRow(cfg.Appearance.Sidebar.AgentRow), ParseSidebarAgentRow(again.Appearance.Sidebar.AgentRow)
	if before.Fingerprint != after.Fingerprint {
		t.Fatalf("the table changed through a save:\n before %s\n after  %s\n%s", before.Fingerprint, after.Fingerprint, out)
	}
}
