package main

import (
	"encoding/json"
	"testing"
)

// TestGetWindowCommandKeepsItsJSON: tuios get-window now reads with the
// get-window verb, so a pane holding read may run it. Its --json output keeps
// the shape the client protocol's GetWindow gave it, so a script reading
// .agent_state or .success is unchanged.
func TestGetWindowCommandKeepsItsJSON(t *testing.T) {
	c := startSubscribeDaemon(t)
	if _, err := c.Call("new-session", map[string]any{"name": "gw"}); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	raw, err := c.Call("list-windows", map[string]any{"session": "gw"})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Windows []map[string]any `json:"windows"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list.Windows) == 0 {
		t.Fatalf("list-windows = %s, %v", raw, err)
	}
	id := list.Windows[0]["window_id"].(string)

	var qerr error
	out := captureStdout(t, func() { qerr = queryWindow("gw", []string{id}, true) })
	if qerr != nil {
		t.Fatalf("queryWindow: %v", qerr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q is not JSON: %v", out, err)
	}
	if got["success"] != true || got["message"] != "command executed" || got["window_id"] != id {
		t.Errorf("get-window --json = %v, want success, message and window %s", got, id)
	}
	if _, ok := got["agent_state"]; !ok {
		t.Errorf("get-window --json = %v, want agent_state", got)
	}
	if _, ok := got["type"]; ok {
		t.Errorf("get-window --json carries the verb's type field: %v", got)
	}
}
