package harness

import "testing"

// registryFromTOML builds a registry holding one manifest written inline, so a
// rule shape can be pinned without adding a manifest to the bundled set.
func registryFromTOML(t *testing.T, body string) *Registry {
	t.Helper()
	m, err := parseManifest("inline.toml", []byte(body))
	if err != nil {
		t.Fatalf("parse the inline manifest: %v", err)
	}
	return &Registry{manifests: []*Manifest{m}}
}

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

// titleRegistry is a registry with one harness whose title rules are the two
// shapes the agents actually use.
func titleRegistry(t *testing.T) *Registry {
	t.Helper()
	return registryFromTOML(t, `
schema_version = 1
id = "demo"
[detect]
comm = ["demo-agent"]
[title]
enabled = true
fold_case = true
[[title.rule]]
state = "needs_input"
priority = 10
message = "the title says it is waiting"
any = ["action required"]
[[title.rule]]
state = "working"
priority = 1
any = ["demo"]
`)
}

// TestATitleMovesTheStateOfAClaimedPane is the feature.
func TestATitleMovesTheStateOfAClaimedPane(t *testing.T) {
	reg := titleRegistry(t)

	state, rule, ok := reg.ClassifyTitle("demo", "demo - Action Required: approve?")
	if !ok {
		t.Fatal("a title carrying the blocked phrase matched nothing")
	}
	if state != "needs_input" {
		t.Errorf("state %q, want needs_input", state)
	}
	if got := reg.TitleRuleMessage("demo", rule); got != "the title says it is waiting" {
		t.Errorf("the rule's message is %q", got)
	}
}

// TestTheHighestPriorityTitleRuleWins, the same way the screen tier resolves
// two rules that both match.
func TestTheHighestPriorityTitleRuleWins(t *testing.T) {
	reg := titleRegistry(t)
	// Both rules match this: "demo" and "action required".
	state, _, ok := reg.ClassifyTitle("demo", "demo action required")
	if !ok {
		t.Fatal("nothing matched a title both rules describe")
	}
	if state != "needs_input" {
		t.Errorf("state %q, want the higher-priority needs_input", state)
	}
}

// TestATitleWithNothingToSayIsNoOpinion. A rule that stopped matching has to
// degrade to silence rather than to a confident wrong answer, which is the
// contract the screen tier is held to and for the same reason.
func TestATitleWithNothingToSayIsNoOpinion(t *testing.T) {
	reg := titleRegistry(t)
	for _, title := range []string{"", "vim main.go", "~/src/demo-blinker"} {
		if state, _, ok := reg.ClassifyTitle("demo", title); ok {
			t.Errorf("title %q was classified as %q", title, state)
		}
	}
}

// TestTitleRulesAreOffUntilAManifestAsks. Every manifest that exists was
// written before this, and none of them should start reading titles because
// the code to do it arrived.
func TestTitleRulesAreOffUntilAManifestAsks(t *testing.T) {
	reg := registryFromTOML(t, `
schema_version = 1
id = "quiet"
[detect]
comm = ["quiet-agent"]
[title]
[[title.rule]]
state = "working"
any = ["quiet"]
`)
	if _, _, ok := reg.ClassifyTitle("quiet", "quiet is working"); ok {
		t.Error("a title block that did not say enabled was read anyway")
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

// TestNoOtherBundledManifestReadsTitlesYet. The rule for shipping one is that
// the phrase is unambiguous and the agent writes it deliberately, and a
// spinner is neither. This is here so enabling a second one is a decision
// somebody makes rather than something that happens.
func TestNoOtherBundledManifestReadsTitlesYet(t *testing.T) {
	r := testRegistry(t)
	for _, id := range r.IDs() {
		if id == "codex" {
			continue
		}
		if m := r.Lookup(id); m != nil && m.Title.Enabled {
			t.Errorf("manifest %q reads titles; if that is intended, say why here", id)
		}
	}
}
