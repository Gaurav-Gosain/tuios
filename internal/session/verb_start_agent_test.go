package session

import (
	"strings"
	"testing"
)

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
