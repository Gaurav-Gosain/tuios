package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadinessSignInChecksDoNotRaceTheList asks agent-readiness about
// claude-code, whose sign-in check runs on a goroutine of its own, and then
// about codex. The loop appends codex's row while claude-code's check is out.
// The check must write its answer without reading the list the loop is
// growing: under -race, a goroutine that reads it is a data race.
func TestReadinessSignInChecksDoNotRaceTheList(t *testing.T) {
	bin := t.TempDir()
	for name, script := range map[string]string{
		"claude": "#!/bin/sh\necho '{\"loggedIn\": true}'\n",
		"codex":  "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d := &Daemon{ctx: t.Context(), manager: NewManager(), agentMatcher: newAgentMatcher(nil)}
	ans, verr := d.readiness(nil, readinessParams{
		Harnesses: []string{"claude-code", "codex"},
		Env:       map[string]string{"PATH": bin},
	})
	if verr != nil {
		t.Fatalf("agent-readiness: %v", verr)
	}
	got := map[string]AgentReadiness{}
	for _, a := range ans.Agents {
		got[a.Harness] = a
	}
	if a := got["claude-code"]; !a.Installed || a.SignedIn != SignedInYes {
		t.Errorf("claude-code: installed %v, signed in %q, want installed and %q", a.Installed, a.SignedIn, SignedInYes)
	}
	if a, ok := got["codex"]; !ok || !a.Installed {
		t.Errorf("codex is not listed as installed: %+v", ans.Agents)
	}
}
