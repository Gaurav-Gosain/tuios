package config

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/pelletier/go-toml/v2"
)

const linkRulesTOML = `
[[links.rules]]
regex = '\b([A-Z][A-Z0-9_]+-[1-9][0-9]*)\b'
url = "https://jira.example.com/browse/$1"

[[links.rules]]
regex = '('
url = "https://broken.example/$0"
`

func TestLinkRulesParseAndCompileIntoSettings(t *testing.T) {
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(linkRulesTOML), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Links.Rules) != 2 || cfg.Links.Rules[0].URL != "https://jira.example.com/browse/$1" {
		t.Fatalf("rules = %+v", cfg.Links.Rules)
	}

	var s Settings
	ApplyAppearanceConfig(&cfg, &s)
	got := hints.Links("see PROJ-12", s.LinkRules)
	if len(got) != 1 || got[0].Target != "https://jira.example.com/browse/PROJ-12" {
		t.Fatalf("compiled rules found %+v", got)
	}
}

func TestLinkRulesWarnAboutABrokenRule(t *testing.T) {
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(linkRulesTOML), &cfg); err != nil {
		t.Fatal(err)
	}
	result := &ValidationResult{}
	validateLinks(&cfg, result)
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one for the broken rule", result.Warnings)
	}
	w := result.Warnings[0]
	if w.Field != "links" || w.Key != "rules[1]" || !strings.Contains(w.Message, "skips this link rule") {
		t.Fatalf("warning = %+v", w)
	}
}

func TestLinkRulesWarnAboutAGroupTheRegexDoesNotHave(t *testing.T) {
	cfg := &UserConfig{Links: LinksConfig{Rules: []LinkRuleConfig{
		{Regex: `([A-Z]+-\d+)`, URL: "https://x/$1_y"},
		{Regex: `([A-Z]+-\d+)`, URL: "https://x/${1}_y"},
	}}}
	result := &ValidationResult{}
	validateLinks(cfg, result)
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one for the $1_y rule", result.Warnings)
	}
	w := result.Warnings[0]
	if w.Key != "rules[0]" || !strings.Contains(w.Message, "$1_y") || !strings.Contains(w.Message, "${1}") {
		t.Fatalf("warning = %+v", w)
	}
}

func TestNoLinkRulesCompileToNil(t *testing.T) {
	var s Settings
	ApplyAppearanceConfig(&UserConfig{}, &s)
	if s.LinkRules != nil {
		t.Fatalf("LinkRules = %+v, want nil without rules", s.LinkRules)
	}
}
