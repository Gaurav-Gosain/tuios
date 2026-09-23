package main

import (
	"strings"
	"testing"
)

// TestAgentProtoRefusesAnUnknownProtocol: a protocol it does not speak ends
// the command before anything is started.
func TestAgentProtoRefusesAnUnknownProtocol(t *testing.T) {
	if code := runAgentProto(agentProtoOptions{protocol: "mcp"}, []string{"/nonexistent/agent"}); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}

// TestAgentProtoRequiresAProtocol: the flag is required, so a bare run
// cannot guess one.
func TestAgentProtoRequiresAProtocol(t *testing.T) {
	root := newRootCommand()
	root.SetArgs([]string{"agent-proto", "--", "true"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("err = %v, want the missing --protocol named", err)
	}
}
