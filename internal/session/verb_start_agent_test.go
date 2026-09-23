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

func TestStartAgentMakesTheSessionAndTypesThePromptWhenReady(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	c := dialVerb(t, sp)

	c.send(t, `{"id":1,"verb":"start-agent","params":`+jsonParams(map[string]any{
		"session": "agents", "agent": "claude", "cwd": repo, "args": []string{"--model", "opus"},
		"prompt": "Add a retry to the client.",
	})+`}`)
	sess, windowID := waitOnlyPane(t, d, "agents", 1)
	if err := sess.SetDaemonWindowAgentState(windowID, AgentStateIdle, ""); err != nil {
		t.Fatal(err)
	}
	res := result(t, c.readResp(t))
	if res["created_session"] != true || res["session"] != "agents" {
		t.Errorf("result = %v, want the session agents created", res)
	}
	if res["agent"] != "claude-code" || res["cwd"] != repo || res["ready"] != true {
		t.Errorf("result = %v, want claude-code ready in %s", res, repo)
	}
	if res["command"] != "claude --model opus" {
		t.Errorf("command = %v, want the agent with args after it", res["command"])
	}
	if res["prompt_status"] != PromptSent {
		t.Errorf("prompt_status = %v (%v), want sent", res["prompt_status"], res["prompt_note"])
	}
	waitPaneText(t, d, sess, windowID, "GOT: Add a retry to the client.")

	// A second agent in the same session is a second pane, not a new session.
	c.send(t, `{"id":2,"verb":"start-agent","params":{"session":"agents","agent":"claude"}}`)
	_, second := waitOnlyPane(t, d, "agents", 2)
	if err := sess.SetDaemonWindowAgentState(second, AgentStateIdle, ""); err != nil {
		t.Fatal(err)
	}
	res = result(t, c.readResp(t))
	if res["created_session"] != false {
		t.Errorf("created_session = %v for a session that existed", res["created_session"])
	}
	if _, has := res["prompt_status"]; has {
		t.Errorf("prompt_status = %v with no prompt", res["prompt_status"])
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

func TestStartAgentFindsTheRepositoryByItsOrigin(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	root, checkout := reposRootWith(t, repo, "git@github.com:acme/api.git")
	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"start-agent","params":`+jsonParams(map[string]any{
		"session": "api", "agent": "claude", "repo_url": "https://github.com/acme/api", "repos_root": root, "ready_timeout": 300,
	})+`}`))
	if res["cwd"] != checkout {
		t.Errorf("cwd = %v, want the checkout %s", res["cwd"], checkout)
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
