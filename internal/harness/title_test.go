package harness

import "testing"

// The window title as evidence, and the boundary rule that makes it safe.
//
// A screen is prose and a substring anywhere in it is a fair match. A title is
// mostly paths, branches and program names, so the same test finds an agent's
// name inside words that are not it, and the false positive arrives wearing
// the right label.

// TestContainsToken holds a title predicate to whole tokens.
//
//   - A rule must not match inside a longer word; this is the case the issue
//     names. Negative control: using strings.Contains for a title rule matches
//     here and this fails, which is the whole reason containsToken exists.
//   - The guard has to leave the real matches alone or it has only broken the
//     feature.
//   - A path separator ends a token, so a rule naming a program still matches
//     it where it was installed. The hyphen is deliberately not a boundary,
//     because that is exactly what opencode-blinker turns on.
//   - A predicate that carries its own boundary ("] ", a trailing colon) is
//     matched plainly; holding it to a word boundary as well would refuse
//     every string it was meant for.
//   - An empty predicate matches nothing, or it would match every title.
//   - A bounded occurrence that starts inside an unbounded one is still found.
//     In "xa a a " the "a a" at index 1 is not bounded, the one at 3 is, and a
//     scan that resumes past the needle never sees it. Negative control:
//     advancing by len(needle) fails here.
func TestContainsToken(t *testing.T) {
	for _, tc := range []struct {
		hay, needle string
		want        bool
	}{
		{"~/src/opencode-blinker", "opencode", false},
		{"opencode-blinker", "opencode", false},
		{"vim opencode-blinker/main.go", "opencode", false},
		{"myopencode", "opencode", false},
		{"opencodex", "opencode", false},

		{"opencode", "opencode", true},
		{"opencode ~/src", "opencode", true},
		{"~/bin/opencode", "opencode", true},
		{"running opencode now", "opencode", true},
		{"(opencode)", "opencode", true},
		{"[opencode]", "opencode", true},

		{"/usr/local/bin/codex", "codex", true},
		{"/usr/local/bin/codex-wrapper", "codex", false},

		{"esc to interrupt] working", "] ", true},
		{"Action Required: approve?", "Action Required:", true},

		{"anything", "", false},

		{"xa a a ", "a a", true},
	} {
		if got := containsToken(tc.hay, tc.needle); got != tc.want {
			t.Errorf("containsToken(%q, %q) = %v, want %v", tc.hay, tc.needle, got, tc.want)
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
