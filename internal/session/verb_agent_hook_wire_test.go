package session

import (
	"testing"
)

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

	both := `{"harness":"claude-code","payload":"{}","payload_json":{}}`
	if _, verr := decodeHookCall([]byte(both), "hook"); verr == nil || verr.Code != ErrVerbInvalidParams {
		t.Fatalf("payload and payload_json together: %v, want invalid_params", verr)
	}
}
