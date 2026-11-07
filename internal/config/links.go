package config

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
)

// LinksConfig is the [links] table: rules that turn plain text into links.
// The policy for which links tuios acts on at all is appearance.links; rules
// apply when it is "all", like bare URLs.
type LinksConfig struct {
	// Rules are applied in order. A real URL wins over a rule, and an earlier
	// rule wins over a later one.
	Rules []LinkRuleConfig `toml:"rules"`
}

// LinkRuleConfig is one [[links.rules]] entry.
type LinkRuleConfig struct {
	// Regex is a regular expression in Go syntax.
	Regex string `toml:"regex"`
	// URL is the address a match opens. $0 is the whole match, $1, $2, ...
	// (or ${name}) are its groups.
	URL string `toml:"url"`
}

// Specs returns the rules in the form the detector compiles.
func (c LinksConfig) Specs() []hints.LinkRuleSpec {
	if len(c.Rules) == 0 {
		return nil
	}
	specs := make([]hints.LinkRuleSpec, 0, len(c.Rules))
	for _, r := range c.Rules {
		specs = append(specs, hints.LinkRuleSpec{Regex: r.Regex, URL: r.URL})
	}
	return specs
}

// validateLinks warns about a rule that does not compile. Each is skipped at
// run time, so without a warning a typo would look like the rule being
// ignored.
func validateLinks(cfg *UserConfig, result *ValidationResult) {
	for i, r := range cfg.Links.Rules {
		unknown, err := hints.UnknownGroups(hints.LinkRuleSpec{Regex: r.Regex, URL: r.URL})
		if err != nil {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "links",
				Key:     fmt.Sprintf("rules[%d]", i),
				Message: fmt.Sprintf("tuios skips this link rule: %v.", err),
			})
			continue
		}
		if len(unknown) > 0 {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "links",
				Key:   fmt.Sprintf("rules[%d]", i),
				Message: fmt.Sprintf("The url of this link rule names %s, which its regex does not have, so it expands to nothing. "+
					"A group followed by a letter, a digit or _ needs braces: ${1}.",
					quotedGroups(unknown)),
			})
		}
	}
}

// quotedGroups writes group names as a template would: $1, $name.
func quotedGroups(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "$" + n
	}
	return strings.Join(out, ", ")
}
