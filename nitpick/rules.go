package nitpick

import (
	"slices"

	"golang.org/x/tools/go/analysis"
)

// A Rule is an analyzer with what nitpick knows about it.
type Rule struct {
	Analyzer *analysis.Analyzer
	// Default is true for a rule that runs when no rule is named.
	Default bool
	// Precision is the share of findings that a person judged worth a
	// change, from 0 to 1, on the sample that PRECISION.md describes.
	Precision float64
	// Sample is the number of labelled findings behind Precision.
	Sample int
}

// Name is the rule name, which is also the analyzer name.
func (r Rule) Name() string { return r.Analyzer.Name }

// Rules lists every rule, the default ones first. PRECISION.md holds the
// same numbers, and a test keeps the two equal.
var Rules = []Rule{
	{Analyzer: SwallowedError, Default: true, Precision: 4.0 / 40, Sample: 40},
	{Analyzer: ErrorToEmptySuccess, Default: true, Precision: 6.0 / 11, Sample: 11},
	{Analyzer: ConfigFallback, Default: true, Precision: 0.0 / 1, Sample: 1},
	{Analyzer: NarratorComment, Default: true, Precision: 17.0 / 20, Sample: 20},
	{Analyzer: DeepNesting, Default: true, Precision: 26.0 / 40, Sample: 40},
	{Analyzer: ElisionComment, Precision: 3.0 / 4, Sample: 4},
	{Analyzer: JunkIdentifier, Precision: 2.0 / 6, Sample: 6},
	{Analyzer: CommentedOutCode, Precision: 22.0 / 36, Sample: 36},
	{Analyzer: NolintWithoutReason, Precision: 4.0 / 20, Sample: 20},
}

// Lookup returns the rule with the given name.
func Lookup(name string) (Rule, bool) {
	i := slices.IndexFunc(Rules, func(r Rule) bool { return r.Name() == name })
	if i < 0 {
		return Rule{}, false
	}
	return Rules[i], true
}

// Analyzers returns the analyzers of the default rules.
func Analyzers() []*analysis.Analyzer {
	var out []*analysis.Analyzer
	for _, r := range Rules {
		if r.Default {
			out = append(out, r.Analyzer)
		}
	}
	return out
}
