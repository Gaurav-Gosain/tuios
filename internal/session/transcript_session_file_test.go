package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSessionFileJoin checks the join Amp's plugin makes by naming its thread
// id. The id is a file name under the transcript folder, so it is held to
// the rules of a reported path and to more: the file must exist, the report
// must be applied, and a report with no connection (herdr's socket, which
// any client writes to) names no file.
func TestSessionFileJoin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	threads := filepath.Join(home, ".local", "share", "amp", "threads")
	if err := os.MkdirAll(threads, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "T-00000000-0000-7000-8000-000000000001"
	if err := os.WriteFile(filepath.Join(threads, id+".json"), []byte(`{"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	// Each report goes to a pane of its own: a pane holds to the first
	// session id reported for it.
	n := 0
	pane := func() (string, func() string) {
		n++
		name := "work" + string(rune('a'+n))
		sess := makeSessionWithWindow(t, d, name)
		win := sess.GetState().Windows[0].ID
		return name, func() string {
			path, _, _, _, ok := sess.transcriptJoinOf(win)
			if !ok {
				return ""
			}
			return path
		}
	}
	report := func(name, sid string) map[string]any {
		return map[string]any{"session": name, "window": "0", "state": "working", "harness": "amp", "agent_session_id": sid}
	}

	// herdr reports through the verb with no connection.
	name, joined := pane()
	raw, _ := json.Marshal(report(name, id))
	if _, verr := d.verbSetAgentState(nil, raw); verr != nil {
		t.Fatal(verr.Message)
	}
	if got := joined(); got != "" {
		t.Fatalf("a report with no connection joined %s", got)
	}

	for _, sid := range []string{"T-missing", "../T-x", "..", "T-a/b"} {
		name, joined := pane()
		if res := result(t, callVerb(t, c, "set-agent-state", report(name, sid))); res["applied"] != true {
			t.Fatalf("the report of session id %q was not applied: %v", sid, res)
		}
		if got := joined(); got != "" {
			t.Fatalf("session id %q joined %s", sid, got)
		}
	}
	// A pane that holds another session refuses the report, and a refused
	// report joins nothing.
	name, joined = pane()
	result(t, callVerb(t, c, "set-agent-state", report(name, "T-other")))
	if res := result(t, callVerb(t, c, "set-agent-state", report(name, id))); res["applied"] == true {
		t.Fatalf("a pane holding another session applied the report: %v", res)
	}
	if got := joined(); got != "" {
		t.Fatalf("a refused report joined %s", got)
	}

	name, joined = pane()
	result(t, callVerb(t, c, "set-agent-state", report(name, id)))
	want := filepath.Join(threads, id+".json")
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	if got := joined(); got != want {
		t.Fatalf("joined %q, want %q", got, want)
	}
}
