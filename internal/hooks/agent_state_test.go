package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentStateHookEnvironmentContract runs a real shell so the documented
// contract is checked against what a user's script would actually see, not
// against the struct the runner was handed.
func TestAgentStateHookEnvironmentContract(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "env")

	m := NewManager()
	m.Register(AfterAgentState, "env | grep '^TUIOS_' | sort > "+out)
	m.Fire(AfterAgentState, Context{
		WindowID:       "w-1",
		WindowName:     "build",
		Workspace:      3,
		SessionID:      "main",
		AgentState:     "needs_input",
		PrevAgentState: "working",
		AgentHarness:   "claude",
		AgentMessage:   "awaiting approval",
	})
	m.Wait()

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"TUIOS_EVENT=after-agent-state",
		"TUIOS_WINDOW_ID=w-1",
		"TUIOS_WINDOW_NAME=build",
		"TUIOS_WORKSPACE=3",
		"TUIOS_SESSION_ID=main",
		"TUIOS_AGENT_STATE=needs_input",
		"TUIOS_AGENT_PREV_STATE=working",
		"TUIOS_AGENT_HARNESS=claude",
		"TUIOS_AGENT_MESSAGE=awaiting approval",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from the hook environment:\n%s", want, got)
		}
	}
}

// TestAgentStateHookMessageWithShellMetacharacters is why the contract is
// environment and not argv: an agent message is free text from a harness.
func TestAgentStateHookMessageWithShellMetacharacters(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "msg")
	nasty := `"; rm -rf $HOME; echo '`

	m := NewManager()
	m.Register(AfterAgentState, `printf '%s' "$TUIOS_AGENT_MESSAGE" > `+out)
	m.Fire(AfterAgentState, Context{AgentMessage: nasty})
	m.Wait()

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	if string(data) != nasty {
		t.Errorf("message = %q, want %q", data, nasty)
	}
}
