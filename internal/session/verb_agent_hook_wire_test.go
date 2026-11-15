package session

import (
	"testing"
)

// agent-hook-event has two callers that write its params differently, and
// both are installed on machines now. The hook line tuios integration
// installs (v0.13.0 on) is shell printf, which cannot quote JSON, so it puts
// the payload in as JSON under payload_json and says forwarder. A caller that
// can quote, such as tuios call, sends the payload as a string under payload.
// A daemon that refuses either shape makes every hook event of that caller
// pay a refused call and a start of tuios agent-hook, with no error anyone
// sees. The lines below are what each caller writes, byte for byte.

func TestAgentHookEventTakesBothParamShapes(t *testing.T) {
	const payload = `{"hook_event_name":"Stop","session_id":"conv-1"}`
	cases := []struct {
		name      string
		params    string
		forwarder bool
	}{
		{
			name:      "forwarder line",
			params:    `{"harness":"claude-code","forwarder":true,"env":{"CODEX_THREAD_ID":"t-1"},"payload_json":` + payload + `}`,
			forwarder: true,
		},
		{
			name:   "payload as a string",
			params: `{"harness":"claude-code","payload":"{\"hook_event_name\":\"Stop\",\"session_id\":\"conv-1\"}"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, verr := decodeHookCall([]byte(tc.params), "hook")
			if verr != nil {
				t.Fatalf("refused: %s: %s", verr.Code, verr.Message)
			}
			if p.Payload == nil || *p.Payload != payload {
				t.Fatalf("payload = %v, want %s", p.Payload, payload)
			}
			if p.Forwarder != tc.forwarder {
				t.Fatalf("forwarder = %v, want %v", p.Forwarder, tc.forwarder)
			}
		})
	}

	// Both at once is ambiguous and is refused, so neither silently wins.
	both := `{"harness":"claude-code","payload":"{}","payload_json":{}}`
	if _, verr := decodeHookCall([]byte(both), "hook"); verr == nil || verr.Code != ErrVerbInvalidParams {
		t.Fatalf("payload and payload_json together: %v, want invalid_params", verr)
	}
}
