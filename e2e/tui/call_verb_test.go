package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// tuios call sends one verb with its params and prints the daemon's reply as
// JSON, for a program that cannot write to the socket itself, such as a
// Tern plugin, which can only start processes.
//
// What it must do, and how each check could pass wrongly:
//   - Print the result object exactly as the daemon sends it. The check reads
//     a field only the daemon's list-sessions result has (window_count), so
//     a wrapper that printed its own summary fails it.
//   - Pass the params through. new-window with a name, then list-windows
//     finds that name, so params dropped on the way fail it.
//   - Print a daemon error as JSON with its stable code, and exit non-zero.
//     The code is the daemon's, not text the CLI made up.
//   - Refuse the verbs that turn the connection into a stream, which a
//     one-shot call would leave hanging.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "tuios call"):
//   - with the params left out of the call, TestCallVerb fails at "params did
//     not reach the daemon".
func TestCallVerb(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "callme", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "call", "list-sessions")
	if err != nil {
		t.Fatalf("ASSERTION: tuios call list-sessions failed: %v: %s", err, out)
	}
	var sessions struct {
		Type     string `json:"type"`
		Sessions []struct {
			Name        string `json:"name"`
			WindowCount int    `json:"window_count"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("ASSERTION: the output is not the daemon's JSON: %v: %s", err, out)
	}
	if sessions.Type != "session_list" || len(sessions.Sessions) != 1 || sessions.Sessions[0].Name != "callme" || sessions.Sessions[0].WindowCount != 1 {
		t.Fatalf("ASSERTION: list-sessions through call is not the daemon's result: %s", out)
	}

	if out, err := tuiosCLI(t, base, "call", "new-window", `{"session":"callme","name":"through-call"}`); err != nil {
		t.Fatalf("ASSERTION: new-window through call failed: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "call", "list-windows", `{"session":"callme"}`)
	if err != nil {
		t.Fatalf("list-windows: %v: %s", err, out)
	}
	if !strings.Contains(out, `"through-call"`) {
		t.Fatalf("ASSERTION: params did not reach the daemon: no window named through-call in %s", out)
	}

	out, err = tuiosCLI(t, base, "call", "list-windows", `{"session":"no-such-session"}`)
	if err == nil {
		t.Fatalf("ASSERTION: a verb error exited zero: %s", out)
	}
	var verr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &verr); jerr != nil || verr.Error.Code != "session_not_found" {
		t.Fatalf("ASSERTION: a verb error is not printed as JSON with the daemon's code: %s", out)
	}

	for _, verb := range []string{"subscribe", "stream-pane", "open-file-stream"} {
		out, err := tuiosCLI(t, base, "call", verb, `{}`)
		if err == nil || !strings.Contains(out, "stream") {
			t.Fatalf("ASSERTION: call %s was not refused as a stream verb: %v: %s", verb, err, out)
		}
	}
}
