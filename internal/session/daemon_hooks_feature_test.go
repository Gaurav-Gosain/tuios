//go:build !slim

package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestTheAgentStateHookObeysTheAlertPolicy keeps the meaning of the event: it
// is the only one gated by config rather than by the raw fact, and moving it to
// the daemon must not quietly turn a muted state back on.
func TestTheAgentStateHookObeysTheAlertPolicy(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))

	off := false
	d := NewDaemon(&DaemonConfig{
		Version:            "test",
		DisableAutoRestore: true,
		Hooks:              map[string]any{string(hooks.AfterAgentState): "true"},
		// No settle window, so the assertion is about the policy and not about a
		// timer. suppress_focused off, because the pane under test is the
		// focused one and this is not the test for that rule.
		AgentAlerts: &config.AgentAlertsConfig{
			SettleSeconds:   intPtr(0),
			SuppressFocused: &off,
		},
	})
	rec := &hookRecorder{}
	d.hooks.SetRunner(rec.add)
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)
	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	makeSessionWithWindow(t, d, "agents")
	c := dialVerb(t, sp)

	// working is muted by default, so it must not reach the command.
	result(t, c.call(t, `{"verb":"set-agent-state","params":{"session":"agents","state":"working"}}`))
	rec.settle()
	if got := rec.of(hooks.AfterAgentState); len(got) != 0 {
		t.Fatalf("a muted state fired the hook %d times: %+v", len(got), got)
	}

	// done alerts by default, so it must.
	result(t, c.call(t, `{"verb":"set-agent-state","params":{"session":"agents","state":"done","harness":"claude-code"}}`))
	got := rec.await(t, hooks.AfterAgentState, 1)[0]
	if got.AgentState != "done" {
		t.Errorf("hook agent state = %q, want done", got.AgentState)
	}
	if got.PrevAgentState != "working" {
		t.Errorf("hook previous agent state = %q, want the working it came from", got.PrevAgentState)
	}
	if got.AgentHarness != "claude-code" {
		t.Errorf("hook harness = %q, want claude-code", got.AgentHarness)
	}
}
