package session

import "testing"

// TestAskpassEnvFollowsTheAgentSwitch: with the agent features off the
// prompts have no Inbox to go to, so a pane gets no SSH_ASKPASS and ssh
// behaves as it did without tuios.
//
// Negative control: with the agent check in AskpassEnv cut, the env is set
// with the features off.
func TestAskpassEnvFollowsTheAgentSwitch(t *testing.T) {
	m := &Manager{}
	m.SetAskpassBin("/run/tuios/askpass/bin/tuios-askpass")
	on := true
	m.SetAskpassAgentsOn(func() bool { return on })
	if env := m.AskpassEnv(); len(env) != 2 {
		t.Fatalf("with the agent features on, AskpassEnv = %q, want SSH_ASKPASS and SSH_ASKPASS_REQUIRE", env)
	}
	on = false
	if env := m.AskpassEnv(); env != nil {
		t.Fatalf("with the agent features off, AskpassEnv = %q, want none", env)
	}
	on = true
	m.SetAskpass("off")
	if env := m.AskpassEnv(); env != nil {
		t.Fatalf("with [agents] askpass off, AskpassEnv = %q, want none", env)
	}
}
