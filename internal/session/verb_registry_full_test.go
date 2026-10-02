//go:build !slim

package session

import (
	"slices"
	"testing"
)

// TestSlimDroppedVerbsAreFeatureVerbs holds slimDroppedVerbs, the names a
// slim daemon answers with "not in tuios-slim", equal to the verbs the full
// build registers through addFeatureVerbs. A verb added to the feature
// files without the list would get the plain unknown_verb answer in slim.
func TestSlimDroppedVerbsAreFeatureVerbs(t *testing.T) {
	m := map[string]verbEntry{}
	addFeatureVerbs(m)
	var got []string
	for name := range m {
		got = append(got, name)
		if _, ok := verbRegistry[name]; !ok {
			t.Errorf("feature verb %q is not in the registry", name)
		}
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(slimDroppedVerbs))
	if !slices.Equal(got, want) {
		t.Fatalf("slimDroppedVerbs does not match addFeatureVerbs\n got: %v\nwant: %v", got, want)
	}
	if missingVerbError("send-keys") != nil || missingVerbError("list-agents") != nil {
		t.Fatal("the full build reports a verb as missing")
	}
}
