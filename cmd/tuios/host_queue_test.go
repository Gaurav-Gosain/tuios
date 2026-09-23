package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestHostsSaysMailWaitsForAMachine.
func TestHostsSaysMailWaitsForAMachine(t *testing.T) {
	raw := json.RawMessage(`{"hosts":[{"host":"build","addr":"b","status":"unreachable","queued":2}],"total":1}`)
	var out bytes.Buffer
	if err := printHostList(&out, raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "build: 2 message(s) wait here for the link") {
		t.Errorf("tuios hosts does not say mail waits:\n%s", out.String())
	}
}

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
