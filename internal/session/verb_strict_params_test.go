package session

import (
	"encoding/json"
	"testing"
)

// TestUnknownParamIsRefused pins the answer to a parameter the verb does not
// take. Dropping it is what encoding/json does by default, and it made
// new-window report a created window while ignoring the workspace it was asked
// to put it on: a success envelope for a call that did not happen.
func TestUnknownParamIsRefused(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "strict")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"new-window","params":{"session":"strict","nonesuch":2}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	e := resp["error"].(map[string]any)
	hint, ok := e["hint"].(map[string]any)
	if !ok {
		t.Fatal("refusal carried no hint")
	}
	if hint["param"] != "nonesuch" {
		t.Errorf("hint.param = %v, want nonesuch", hint["param"])
	}
	// The hint has to name what the verb does take, so a caller can recover
	// without a second round trip.
	accepted, _ := hint["accepted"].([]any)
	if len(accepted) == 0 {
		t.Error("hint listed no accepted parameters")
	}

	// And nothing was created: a refused call must not half-run.
	res := result(t, c.call(t, `{"id":2,"verb":"list-windows","params":{"session":"strict"}}`))
	if res["total"] != float64(1) {
		t.Errorf("window count = %v, want the one window the session started with", res["total"])
	}
}

// TestDeclaredParamsAreAllAccepted guards the schema against the strictness it
// now backs: a parameter a handler reads but list-verbs does not declare became
// unreachable the moment unknown names were refused. Driving every declared
// parameter of every verb is what catches that, and it caught two (capture-pane
// scrollback/ansi and wait-for source) when the check went in.
func TestDeclaredParamsAreAllAccepted(t *testing.T) {
	for name, entry := range verbRegistry {
		for _, p := range entry.params {
			if verr := checkParamNames(name, entry, json.RawMessage(`{"`+p.Name+`":null}`)); verr != nil {
				t.Errorf("verb %s refuses its own declared parameter %s: %v", name, p.Name, verr)
			}
		}
	}
}
