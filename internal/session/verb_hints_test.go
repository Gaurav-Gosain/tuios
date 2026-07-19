package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// errorOf extracts the error object from a response, failing when the response
// carried a result instead.
func errorOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error envelope, got: %v", resp)
	}
	return e
}

// hintOf extracts the hint object from an error envelope, failing when there is
// none. Every degraded state covered here is expected to name its remedy.
func hintOf(t *testing.T, e map[string]any) map[string]any {
	t.Helper()
	h, ok := e["hint"].(map[string]any)
	if !ok {
		t.Fatalf("expected a hint on this error, got: %v", e)
	}
	return h
}

// hintStrings reads a string-list field off a hint.
func hintStrings(t *testing.T, hint map[string]any, field string) []string {
	t.Helper()
	raw, ok := hint[field].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("hint field %q holds a non-string entry: %v", field, v)
		}
		out = append(out, s)
	}
	return out
}

// TestVerbErrorHints is the table of degraded states an agent can hit, and what
// each one must tell it. Every row asserts the stable code plus the specific
// hint fields that make the failure self-explaining: the parameter at fault, the
// values it accepts, the closest name to what was asked for, what does exist,
// and the verb or CLI command that resolves it.
func TestVerbErrorHints(t *testing.T) {
	d, socketPath := startTestDaemon(t)

	sess := makeSessionWithWindow(t, d, "work")
	if _, err := d.manager.CreateSession("scratch", &SessionConfig{}, 80, 24); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess.SetOption("appearance.border_style", "rounded")

	// An empty session exercises the no-windows state without disturbing "work".
	if _, err := d.manager.CreateSession("empty", &SessionConfig{}, 80, 24); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	tests := []struct {
		name string
		req  string

		wantCode string
		// wantMessage are substrings the human message must contain.
		wantMessage []string
		// wantHint are exact string fields the hint must carry.
		wantHint map[string]string
		// wantAccepted and wantAvailable are membership checks on list fields.
		wantAccepted  []string
		wantAvailable []string
	}{
		{
			name:     "unknown verb suggests the closest one and list-verbs",
			req:      `{"id":1,"verb":"list-window"}`,
			wantCode: ErrVerbUnknownVerb,
			wantHint: map[string]string{
				"verb":         "list-verbs",
				"command":      "tuios list-verbs",
				"did_you_mean": "list-windows",
			},
			wantAvailable: []string{"list-windows", "capture-pane", "wait-for"},
		},
		{
			name:     "a request with no verb is told the envelope shape",
			req:      `{"id":1,"params":{}}`,
			wantCode: ErrVerbInvalidRequest,
			wantHint: map[string]string{"param": "verb", "verb": "list-verbs"},
		},
		{
			name:        "unknown session lists the sessions that exist",
			req:         `{"id":1,"verb":"list-windows","params":{"session":"scratchh"}}`,
			wantCode:    ErrVerbSessionNotFound,
			wantMessage: []string{"scratchh", "not found"},
			wantHint: map[string]string{
				"param":        "session",
				"command":      "tuios ls",
				"did_you_mean": "scratch",
			},
			wantAvailable: []string{"work", "scratch", "empty"},
		},
		{
			name:        "unknown window lists the addressable windows",
			req:         `{"id":1,"verb":"send-text","params":{"session":"work","window":"nope","text":"hi"}}`,
			wantCode:    ErrVerbWindowNotFound,
			wantMessage: []string{"nope"},
			wantHint: map[string]string{
				"param":   "window",
				"verb":    "list-windows",
				"command": "tuios list-windows --json",
			},
		},
		{
			name:     "a session with no windows is told how to make one",
			req:      `{"id":1,"verb":"capture-pane","params":{"session":"empty"}}`,
			wantCode: ErrVerbNoWindows,
			wantHint: map[string]string{
				"verb":    "new-window",
				"command": "tuios run-command NewWindow",
			},
		},
		{
			name:        "a missing required parameter is named",
			req:         `{"id":1,"verb":"send-keys","params":{"session":"work"}}`,
			wantCode:    ErrVerbInvalidParams,
			wantMessage: []string{"keys is required"},
			wantHint:    map[string]string{"param": "keys"},
		},
		{
			name:        "an out-of-range parameter is named",
			req:         `{"id":1,"verb":"resize","params":{"session":"work","width":0,"height":10}}`,
			wantCode:    ErrVerbInvalidParams,
			wantMessage: []string{"positive"},
			wantHint:    map[string]string{"param": "width"},
		},
		{
			name:     "a parameter with a closed value set lists the values",
			req:      `{"id":1,"verb":"wait-for","params":{"condition":"window-outpt","session":"work"}}`,
			wantCode: ErrVerbInvalidParams,
			wantHint: map[string]string{
				"param":        "condition",
				"did_you_mean": "window-output",
			},
			wantAccepted: waitConditions,
		},
		{
			name:     "an unknown capture source lists the sources that exist",
			req:      `{"id":1,"verb":"capture-pane","params":{"session":"work","source":"visable"}}`,
			wantCode: ErrVerbInvalidParams,
			wantHint: map[string]string{
				"param":        "source",
				"did_you_mean": "visible",
			},
			wantAccepted: captureSources,
		},
		{
			name:        "the retired capture source says why it went away",
			req:         `{"id":1,"verb":"capture-pane","params":{"session":"work","source":"recent-unwrapped"}}`,
			wantCode:    ErrVerbInvalidParams,
			wantMessage: []string{"recent-unwrapped"},
			wantHint: map[string]string{
				"param": "source",
				// did_you_mean here proves the retired-value branch ran: the
				// edit distance from "recent-unwrapped" to "recent" is far
				// outside closestMatch's tolerance, so the generic path could
				// never suggest it.
				"did_you_mean": "recent",
				"detail":       retiredCaptureSources["recent-unwrapped"],
			},
			wantAccepted: captureSources,
		},
		{
			name:     "a wrongly typed parameter points at the schema",
			req:      `{"id":1,"verb":"resize","params":{"width":"wide"}}`,
			wantCode: ErrVerbInvalidParams,
			wantHint: map[string]string{"verb": "list-verbs"},
		},
		{
			name:        "kill-session refuses to guess and lists the candidates",
			req:         `{"id":1,"verb":"kill-session","params":{}}`,
			wantCode:    ErrVerbInvalidParams,
			wantMessage: []string{"session is required"},
			wantHint: map[string]string{
				"param":   "session",
				"command": "tuios ls",
			},
			wantAvailable: []string{"work", "scratch", "empty"},
		},
		{
			name:        "an unset option lists the options that are set",
			req:         `{"id":1,"verb":"get-option","params":{"session":"work","key":"appearance.border_styl"}}`,
			wantCode:    ErrVerbOptionNotFound,
			wantMessage: []string{"appearance.border_styl", "work"},
			wantHint: map[string]string{
				"param":        "key",
				"verb":         "set-option",
				"did_you_mean": "appearance.border_style",
			},
			wantAvailable: []string{"appearance.border_style"},
		},
		{
			name:        "a wait that times out says how to see what happened",
			req:         `{"id":1,"verb":"wait-for","params":{"condition":"window-output","session":"work","pattern":"never-appears-xyz","timeout":150}}`,
			wantCode:    ErrVerbTimeout,
			wantMessage: []string{"never-appears-xyz"},
			wantHint: map[string]string{
				"param": "pattern",
				"verb":  "capture-pane",
			},
		},
		{
			name:     "unsubscribing without a stream points at subscribe",
			req:      `{"id":1,"verb":"unsubscribe"}`,
			wantCode: ErrVerbInvalidRequest,
			wantHint: map[string]string{"verb": "subscribe"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh connection per row: some rows leave connection state
			// (subscriptions) behind, and rows must not influence each other.
			c := dialVerb(t, socketPath)
			resp := c.call(t, tc.req)
			e := errorOf(t, resp)

			if code, _ := e["code"].(string); code != tc.wantCode {
				t.Fatalf("code = %v, want %v (full error: %v)", e["code"], tc.wantCode, e)
			}

			message, _ := e["message"].(string)
			if message == "" {
				t.Error("error carries no human message")
			}
			for _, want := range tc.wantMessage {
				if !strings.Contains(message, want) {
					t.Errorf("message %q does not contain %q", message, want)
				}
			}

			hint := hintOf(t, e)
			for field, want := range tc.wantHint {
				if got, _ := hint[field].(string); got != want {
					t.Errorf("hint[%s] = %q, want %q (full hint: %v)", field, got, want, hint)
				}
			}
			for _, want := range tc.wantAccepted {
				if !slices.Contains(hintStrings(t, hint, "accepted"), want) {
					t.Errorf("hint accepted list missing %q: %v", want, hint["accepted"])
				}
			}
			for _, want := range tc.wantAvailable {
				if !slices.Contains(hintStrings(t, hint, "available"), want) {
					t.Errorf("hint available list missing %q: %v", want, hint["available"])
				}
			}
		})
	}
}

// TestNeedsClientHintNamesTheAttachCommand covers the one degraded state that
// cannot be reproduced through a parameter mistake: a verb that genuinely needs
// a renderer, called against a session with no attached client.
func TestNeedsClientHintNamesTheAttachCommand(t *testing.T) { //nolint:revive
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	// Rendering-dependent commands reach this classification through
	// executeDaemonCommand's default branch. Drive the classification directly:
	// no verb in the registry routes one headlessly today, and this keeps the
	// mapping honest the moment one does.
	sess := d.manager.GetSession("work")
	verr := mapResolveErr(errNeedsClient{verb: "ToggleTiling"}, sess)
	if verr.Code != ErrVerbNeedsClient {
		t.Fatalf("code = %s, want %s", verr.Code, ErrVerbNeedsClient)
	}
	if verr.Hint == nil || verr.Hint.Command != "tuios attach work" {
		t.Fatalf("hint should name the attach command for this session, got %+v", verr.Hint)
	}
	if !strings.Contains(verr.Message, "ToggleTiling") {
		t.Errorf("message should name the command that needs a client, got %q", verr.Message)
	}
}

// TestHintsAreOmittedWhenEmpty guards the backward-compatibility promise: an
// error with nothing useful to suggest must not carry an empty hint object,
// because a consumer checking for the field's presence would be misled.
func TestHintsAreOmittedWhenEmpty(t *testing.T) {
	e := hintedVerbError(ErrVerbInternal, "boom", &VerbHint{})
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "hint") {
		t.Errorf("an empty hint must be omitted, got %s", data)
	}

	// And the legacy constructor must keep producing a hint-free envelope.
	data, err = json.Marshal(newVerbError(ErrVerbInternal, "boom"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(data), `{"code":"internal","message":"boom"}`; got != want {
		t.Errorf("envelope = %s, want %s", got, want)
	}
}

// TestClosestMatch pins the suggestion policy, which is what keeps did_you_mean
// useful rather than noisy: near misses are suggested, unrelated names are not,
// and a name that is already correct is never suggested back.
func TestClosestMatch(t *testing.T) {
	verbs := []string{"list-windows", "list-sessions", "new-window", "close-window", "capture-pane"}

	tests := []struct {
		target string
		want   string
	}{
		{"list-window", "list-windows"},   // one deletion
		{"list-sesions", "list-sessions"}, // one deletion mid-word
		{"New-Window", "new-window"},      // case only
		{"capture-pain", "capture-pane"},  // one substitution
		{"list-windows", ""},              // already correct: nothing to suggest
		{"", ""},                          // nothing asked for
		{"totally-unrelated-thing", ""},   // too far from everything
		{"xyz", ""},                       // short and unrelated
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.target), func(t *testing.T) {
			if got := closestMatch(tc.target, verbs); got != tc.want {
				t.Errorf("closestMatch(%q) = %q, want %q", tc.target, got, tc.want)
			}
		})
	}

	if got := closestMatch("anything", nil); got != "" {
		t.Errorf("closestMatch with no candidates = %q, want empty", got)
	}
}
