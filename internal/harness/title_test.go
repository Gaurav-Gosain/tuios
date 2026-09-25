package harness

import "testing"

// The window title as evidence, and the boundary rule that makes it safe.
//
// A screen is prose and a substring anywhere in it is a fair match. A title is
// mostly paths, branches and program names, so the same test finds an agent's
// name inside words that are not it, and the false positive arrives wearing
// the right label.

// TestATitleRuleDoesNotMatchInsideALongerWord is the case the issue names.
//
// Negative control: using strings.Contains for a title rule matches here and
// this fails, which is the whole reason containsToken exists.
func TestATitleRuleDoesNotMatchInsideALongerWord(t *testing.T) {
	for _, hay := range []string{
		"~/src/opencode-blinker",
		"opencode-blinker",
		"vim opencode-blinker/main.go",
		"myopencode",
		"opencodex",
	} {
		if containsToken(hay, "opencode") {
			t.Errorf("a rule for opencode matched %q, which is a different word", hay)
		}
	}
}

// TestATitleRuleMatchesTheWholeToken is the other half: the guard has to leave
// the real matches alone or it has only broken the feature.
func TestATitleRuleMatchesTheWholeToken(t *testing.T) {
	for _, hay := range []string{
		"opencode",
		"opencode ~/src",
		"~/bin/opencode",
		"running opencode now",
		"(opencode)",
		"[opencode]",
	} {
		if !containsToken(hay, "opencode") {
			t.Errorf("a rule for opencode did not match %q, which is the program", hay)
		}
	}
}

// TestAPathSeparatorIsABoundary, so a rule naming a program still matches it
// where it was installed. The hyphen is deliberately not one, because that is
// exactly what opencode-blinker turns on.
func TestAPathSeparatorIsABoundary(t *testing.T) {
	if !containsToken("/usr/local/bin/codex", "codex") {
		t.Error("a path separator did not end the token")
	}
	if containsToken("/usr/local/bin/codex-wrapper", "codex") {
		t.Error("a hyphen ended the token, so codex-wrapper matched a rule for codex")
	}
}

// TestAPredicateWithItsOwnBoundaryIsMatchedPlainly. A rule that says "] " has
// written the boundary into itself, and holding the space to a word boundary
// as well would refuse every string it was meant for.
func TestAPredicateWithItsOwnBoundaryIsMatchedPlainly(t *testing.T) {
	if !containsToken("esc to interrupt] working", "] ") {
		t.Error("a predicate that carries its own boundary was refused")
	}
	if !containsToken("Action Required: approve?", "Action Required:") {
		t.Error("a predicate ending in a colon was refused")
	}
}

// TestAnEmptyPredicateMatchesNothing. A rule naming an empty string would
// otherwise match every title there is.
func TestAnEmptyPredicateMatchesNothing(t *testing.T) {
	if containsToken("anything", "") {
		t.Error("an empty predicate matched")
	}
}

// TestAnOverlappingCandidateIsNotSteppedOver. Advancing by the needle's length
// would skip a bounded occurrence that starts inside an unbounded one.
//
// Negative control: advancing by len(needle) fails here.
func TestAnOverlappingCandidateIsNotSteppedOver(t *testing.T) {
	// Two occurrences of "a a" overlap in "xa a a ": one at index 1, which is
	// not bounded because an x precedes it, and one at index 3, which is. The
	// second starts inside the first, so a scan that resumes past the needle
	// never sees it.
	if !containsToken("xa a a ", "a a") {
		t.Error("a bounded occurrence overlapping an unbounded one was missed")
	}
}

// TestTheShippedCodexTitleRuleReadsItsPhrase. The one title rule that ships
// enabled, against the phrase Codex actually writes and against the strings a
// careless rule would also take.
func TestTheShippedCodexTitleRuleReadsItsPhrase(t *testing.T) {
	r := testRegistry(t)

	for _, title := range []string{
		"Codex - Action Required",
		"action required: approve the patch?",
		"~/src/api — Action Required",
	} {
		state, _, ok := r.ClassifyTitle("codex", title)
		if !ok || state != "needs_input" {
			t.Errorf("title %q classified as %q (matched=%v), want needs_input", title, state, ok)
		}
	}

	// A title that says nothing about waiting says nothing at all.
	for _, title := range []string{
		"codex",
		"~/src/codex-playground",
		"vim action_required.md",
	} {
		if state, _, ok := r.ClassifyTitle("codex", title); ok {
			t.Errorf("title %q was classified as %q", title, state)
		}
	}
}
