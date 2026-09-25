package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// progressHarness is a manifest whose harness uses OSC 9;4 its own way:
// indeterminate progress means it waits on the user, which the sequence's
// published meaning would call working. It also submits on a line feed and
// asks for a focus report, so the input profile lookup has something to find.
const progressHarness = `schema_version = 1
id = "prog-agent"
[detect]
comm = ["prog-agent"]
[title]
enabled = true
[[title.rule]]
state   = "needs_input"
kind    = "approval"
message = "Waits for you, by its progress report."
region  = "osc_progress"
regex   = ['^4;3$']
[input]
submit = "lf"
focus_before_submit = true
source = "test"
`

func progressRegistry(t *testing.T) *harness.Registry {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prog-agent.toml"), []byte(progressHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, errs := harness.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	return reg
}

// TestProgressRulesOutrankThePublishedMeaning: a harness whose manifest reads
// osc_progress is read by its own rules when they have an answer, and gets the
// sequence's published meaning when they do not. A harness with no such rule
// is untouched.
func TestProgressRulesOutrankThePublishedMeaning(t *testing.T) {
	reg := progressRegistry(t)

	t.Run("a rule with an answer decides", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "prog-agent", AgentStateWorking)
		pty := sess.GetPTY(ptyID)
		pty.storeAgentProgress(vt.ProgressIndeterminate, 0)
		state, _ := pty.takeAgentProgress()
		sess.applyPaneProgress(ptyID, winID, state, reg)
		if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
			t.Fatalf("state = %q, want needs_input from the harness's own rule", got)
		}
	})

	t.Run("a report the rules do not read gets the published meaning", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "prog-agent", AgentStateUnknown)
		pty := sess.GetPTY(ptyID)
		pty.storeAgentProgress(vt.ProgressNormal, 40)
		state, _ := pty.takeAgentProgress()
		sess.applyPaneProgress(ptyID, winID, state, reg)
		if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
			t.Fatalf("state = %q, want working from the published meaning", got)
		}
	})

	t.Run("a harness without progress rules gets the published meaning", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateUnknown)
		pty := sess.GetPTY(ptyID)
		pty.storeAgentProgress(vt.ProgressIndeterminate, 0)
		state, _ := pty.takeAgentProgress()
		sess.applyPaneProgress(ptyID, winID, state, reg)
		if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
			t.Fatalf("state = %q, want working", got)
		}
	})
}
