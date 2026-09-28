package config

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
)

// HintsConfig is the [hints] section: what hints mode looks for on a pane and
// how it labels what it finds. Hints mode puts a short label on every URL,
// path, hash and address on the focused pane, and typing a label copies it.
//
// Nothing here is session state. It is read when hints mode opens, so an edit
// to the file is in force the next time it is opened.
type HintsConfig struct {
	// Builtins names the built-in patterns in use: "all", "none", or a comma
	// separated list such as "url,path,sha" (default: all).
	Builtins string `toml:"builtins"`
	// Patterns are more regular expressions to look for, in Go syntax. A
	// group named match narrows what is copied: "branch: (?P<match>\S+)".
	Patterns []string `toml:"patterns"`
	// Alphabet is the letters labels are made of, nearest match first
	// (default: asdfghjkl). Lowercase letters only.
	Alphabet string `toml:"alphabet"`
	// Open lets Ctrl and a label open a URL or a path (default: true).
	Open *bool `toml:"open"`
	// Dim is the percent of its light the text around the matches loses
	// (default: 60).
	Dim int `toml:"dim"`
}

// Hints defaults and bounds, one source for DefaultConfig, the registry and
// the accessors.
const (
	HintsDefaultBuiltins = "all"
	HintsDefaultDim      = 60
	HintsMinDim          = 10
	HintsMaxDim          = 90
)

// defaultHintsConfig returns the section DefaultConfig carries.
func defaultHintsConfig() HintsConfig {
	return HintsConfig{
		Builtins: HintsDefaultBuiltins,
		Alphabet: hints.DefaultAlphabet,
		Dim:      HintsDefaultDim,
	}
}

// fillMissingHints fills an absent value with its default. Open stays nil,
// which OpenEnabled reads as on.
func fillMissingHints(cfg, defaultCfg *UserConfig) {
	h, d := &cfg.Hints, &defaultCfg.Hints
	if strings.TrimSpace(h.Builtins) == "" {
		h.Builtins = d.Builtins
	}
	if strings.TrimSpace(h.Alphabet) == "" {
		h.Alphabet = d.Alphabet
	}
	if h.Dim <= 0 {
		h.Dim = d.Dim
	}
}

// OpenEnabled reports whether Ctrl and a label may open what it names.
func (h HintsConfig) OpenEnabled() bool { return h.Open == nil || *h.Open }

// DimPercent is the effective dim, held to its range.
func (h HintsConfig) DimPercent() int {
	if h.Dim <= 0 {
		return HintsDefaultDim
	}
	return min(max(h.Dim, HintsMinDim), HintsMaxDim)
}

// LabelAlphabet is the effective alphabet.
func (h HintsConfig) LabelAlphabet() string { return hints.NormalizeAlphabet(h.Alphabet) }

// droppedAlphabet is every character of an alphabet that labels cannot use:
// anything but a to z (upper case counts as its lower case letter).
func droppedAlphabet(a string) string {
	var b strings.Builder
	for _, r := range a {
		if l := unicode.ToLower(r); l < 'a' || l > 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// validateHints warns about a built-in name nobody knows, a pattern that does
// not compile, and an alphabet that falls back to the default. Each is
// skipped at run time, so without a warning a typo would look like hints mode
// ignoring the config.
func validateHints(cfg *UserConfig, result *ValidationResult) {
	h := cfg.Hints
	if _, unknown := hints.ParseBuiltins(h.Builtins); len(unknown) > 0 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "hints",
			Key:   "builtins",
			Message: fmt.Sprintf("Unknown built-in pattern %s. Use all, none, or names from: %s.",
				strings.Join(unknown, ", "), strings.Join(hints.Names(), ", ")),
		})
	}
	for _, expr := range h.Patterns {
		if _, _, err := hints.CompilePattern(expr); err != nil {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "hints",
				Key:     "patterns",
				Message: fmt.Sprintf("Hints mode skips this pattern: %v.", err),
			})
		}
	}
	a := strings.TrimSpace(h.Alphabet)
	switch {
	case a == "":
	case hints.NormalizeAlphabet(a) == hints.DefaultAlphabet && !strings.EqualFold(a, hints.DefaultAlphabet):
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "hints",
			Key:     "alphabet",
			Message: fmt.Sprintf("The alphabet %q has fewer than two letters a to z. Hints mode uses %q.", a, hints.DefaultAlphabet),
		})
	default:
		if dropped := droppedAlphabet(a); dropped != "" {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "hints",
				Key:   "alphabet",
				Message: fmt.Sprintf("Labels use only the letters a to z. Hints mode does not use %q from the alphabet. It uses %q.",
					dropped, hints.NormalizeAlphabet(a)),
			})
		}
	}
}
