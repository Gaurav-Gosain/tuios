package config

import (
	"slices"
	"strings"
	"testing"
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
