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
