package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestOnlyAnUnreachableHostQueuesMail: send-agent-message falls back to the
// daemon's outbox only when the host's link is down. A wrong name or a refusal
// is reported, never queued.
func TestOnlyAnUnreachableHostQueuesMail(t *testing.T) {
	for code, want := range map[string]bool{
		session.ErrVerbHostUnreachable: true,
		session.ErrVerbUnknownHost:     false,
		session.ErrVerbHostRefused:     false,
	} {
		err := explainTargetConnectError("build", "build:api", "", &session.HostConnectError{Host: "build", Code: code, Message: "x"})
		if got := hostUnreachable(err); got != want {
			t.Errorf("%s: hostUnreachable = %v, want %v", code, got, want)
		}
	}
}
