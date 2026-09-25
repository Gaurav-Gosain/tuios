package session

import (
	"testing"
)

// Mail for a machine whose link is down. See host_outbox.go.

// TestALinkCallerCannotQueueMailOnward: host on send-agent-message sends as
// this machine, so a caller that came over a link may not use it.
func TestALinkCallerCannotQueueMailOnward(t *testing.T) {
	_, sp := startTestDaemon(t)
	link := dialLink(t, sp)
	mustRefuse(t, callVerb(t, link, "send-agent-message", map[string]any{"host": "build", "session": "far", "text": "relay me"}),
		ErrVerbForbidden, "a link caller sending onward with host")
}
