package session

import (
	"strings"
	"testing"
	"time"
)

// waitPaneText waits for text on a pane's screen.
func waitPaneText(t *testing.T, d *Daemon, sess *Session, windowID, text string) {
	t.Helper()
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(pty.CaptureContent(true, false), text) {
		if time.Now().After(deadline) {
			t.Fatalf("%q never appeared on the pane:\n%s", text, pty.CaptureContent(true, false))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitOnlyPane waits for the session name to exist with n windows, and
// returns the session and its newest window. start-agent holds its reply
// until the agent is ready, so a test finds the pane from the daemon's side
// and makes it ready.
func waitOnlyPane(t *testing.T, d *Daemon, name string, n int) (*Session, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sess := d.manager.GetSession(name); sess != nil {
			if ws := sess.GetState().Windows; len(ws) == n {
				return sess, ws[n-1].ID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never had %d windows", name, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartAgentThatIsNeverReadyTypesNothing(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"start-agent","params":`+jsonParams(map[string]any{
		"session": "never", "agent": "claude", "cwd": repo, "prompt": "Fix it.", "ready_timeout": 300,
	})+`}`))
	if res["ready"] != false || res["prompt_status"] != PromptNotSent {
		t.Errorf("result = %v, want not ready and the prompt not sent", res)
	}
	if note, _ := res["prompt_note"].(string); !strings.Contains(note, "send-text") {
		t.Errorf("prompt_note = %q, want it to say how to send the prompt by hand", note)
	}
}

func TestStartAgentRefusesWhatItCannotDo(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	c := dialVerb(t, sp)
	cases := map[string]map[string]any{
		"program not on PATH": {"session": "x", "agent": "not-an-agent-on-path"},
		"no agent":            {"session": "x"},
		"cwd and repo":        {"session": "x", "agent": "claude", "cwd": repo, "repo": repo},
		"missing cwd":         {"session": "x", "agent": "claude", "cwd": "/no/such/dir"},
		"bad session name":    {"session": "a/b", "agent": "claude"},
		"clone without url":   {"session": "x", "agent": "claude", "clone": true},
		"negative workspace":  {"session": "x", "agent": "claude", "workspace": -1},
	}
	for name, params := range cases {
		resp := c.call(t, `{"id":1,"verb":"start-agent","params":`+jsonParams(params)+`}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("%s: code = %q, want %q", name, code, ErrVerbInvalidParams)
		}
	}
	// A refused call makes nothing.
	if d.manager.GetSession("x") != nil {
		t.Error("a refused start-agent left a session behind")
	}
}
