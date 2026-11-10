package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// TestAnswerToastNamesOnlyWhatTheAgentTook is a race regression (Mac round 1,
// W-M1). The card draws a held approval; before the answer lands, the hold
// ends (the hook timed out or was stopped), or the screen prompt does not
// move. The toast said "api-server: Allow once" in both cases while the agent
// still waited, so the person thought the call ran. The toast may name an
// answer only when the daemon says the agent took that answer, and it names
// the daemon's decision, which is the one the hook prints.
func TestAnswerToastNamesOnlyWhatTheAgentTook(t *testing.T) {
	noHold := &session.VerbCallError{
		Code:    "invalid_params",
		Message: "the hold on request 19aa00057c8cabaf ended (caller_gone) before this reply",
		Hint:    &session.VerbHint{Param: "request_id", Verb: "list-attention", Command: "tuios list-attention"},
	}
	cases := []struct {
		name, verb, sent string
		raw              string
		err              error
		// taken is whether the agent took the answer; hook is the decision
		// the hook printed, "" for none.
		taken bool
		hook  string
	}{
		{"held, answered", "reply-approval", "once",
			`{"type":"approval_replied","request_id":"r1","decision":"once","applied":true,"reason":"answered"}`, nil, true, "once"},
		{"held, hold ended before the reply", "reply-approval", "once", "", noHold, false, ""},
		{"held, the call changed", "reply-approval", "once",
			`{"type":"approval_replied","request_id":"r1","decision":"","applied":false,"reason":"changed"}`, nil, false, ""},
		{"held, settled earlier as reject", "reply-approval", "once",
			`{"type":"approval_replied","request_id":"r1","decision":"deny","applied":false,"reason":"answered"}`, nil, false, ""},
		{"screen, prompt left", "respond", "once",
			`{"type":"prompt_response","action":"approve","settled_by":"state","state":"working"}`, nil, true, "once"},
		{"screen, a new prompt", "respond", "once",
			`{"type":"prompt_response","action":"approve","settled_by":"prompt","state":"needs_input"}`, nil, true, "once"},
		{"screen, prompt did not move", "respond", "once",
			`{"type":"prompt_response","action":"approve","settled_by":"timeout","state":"needs_input"}`, nil, false, ""},
		{"screen, pane gone", "respond", "deny",
			`{"type":"prompt_response","action":"deny","settled_by":"gone","state":""}`, nil, false, ""},
	}
	words := []string{"once", "always", "deny"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			taken, toast := ckAnswerOutcome(c.verb, "api-server", c.sent, []byte(c.raw), c.err)
			if taken != c.taken {
				t.Errorf("taken = %v, want %v (toast %q)", taken, c.taken, toast)
			}
			for _, w := range words {
				named := strings.Contains(toast, ckDecisionWords(w))
				if named && w != c.hook {
					t.Errorf("toast %q names %q, but the hook printed %q", toast, ckDecisionWords(w), c.hook)
				}
				if !named && w == c.hook {
					t.Errorf("toast %q does not name %q, which the hook printed", toast, ckDecisionWords(w))
				}
			}
			if !c.taken && !strings.Contains(toast, "api-server") {
				t.Errorf("toast %q does not say which agent still waits", toast)
			}
		})
	}
}
