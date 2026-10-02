//go:build slim

package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/edition"
)

// TestSlimLeavesOutFeatureVerbs checks the verbs a slim daemon serves: none
// of slimDroppedVerbs, every core verb, and an unknown_verb answer that names
// tuios-slim for a dropped one.
func TestSlimLeavesOutFeatureVerbs(t *testing.T) {
	for _, name := range slimDroppedVerbs {
		if _, ok := verbRegistry[name]; ok {
			t.Errorf("tuios-slim registers the dropped verb %q", name)
		}
	}
	for _, name := range []string{"hello", "list-verbs", "list-sessions", "new-session", "new-window",
		"split-window", "send-keys", "send-text", "capture-pane", "wait-for", "pane-grants",
		"set-pane-grants", "restrict-connection", "apply-config", "paste-image", "read-dir", "pip"} {
		if _, ok := verbRegistry[name]; !ok {
			t.Errorf("tuios-slim does not register the core verb %q", name)
		}
	}
	verr := missingVerbError("list-agents")
	if verr == nil {
		t.Fatal("list-agents is not reported as missing")
	}
	if verr.Code != ErrVerbUnknownVerb {
		t.Errorf("code = %q, want %q", verr.Code, ErrVerbUnknownVerb)
	}
	if want := "list-agents is not in tuios-slim. Install the full tuios to use it."; verr.Message != want {
		t.Errorf("message = %q, want %q", verr.Message, want)
	}
	if missingVerbError("no-such-verb") != nil {
		t.Error("a verb that never existed is reported as left out of tuios-slim")
	}
	if !strings.Contains(edition.Name, "slim") {
		t.Errorf("edition.Name = %q in a slim build", edition.Name)
	}
}
