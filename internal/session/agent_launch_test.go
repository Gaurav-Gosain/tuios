package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitAgentWords(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		bad  bool
	}{
		{in: "claude", want: []string{"claude"}},
		{in: "  codex   --model o5 ", want: []string{"codex", "--model", "o5"}},
		{in: `aider --message 'fix the "bug"'`, want: []string{"aider", "--message", `fix the "bug"`}},
		{in: `x "a \"b\" \\ c" d\ e`, want: []string{"x", `a "b" \ c`, "d e"}},
		{in: `x ''`, want: []string{"x", ""}},
		// Nothing is expanded: these are literal words.
		{in: `x $HOME $(id) ; rm`, want: []string{"x", "$HOME", "$(id)", ";", "rm"}},
		{in: `x 'open`, bad: true},
		{in: `x \`, bad: true},
	} {
		got, err := splitAgentWords(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("splitAgentWords(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") || len(got) != len(tc.want) {
			t.Errorf("splitAgentWords(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestCallerEnvRefusesWhatItMayNotSet(t *testing.T) {
	for _, env := range []map[string]string{
		{"TUIOS_PANE_ID": "x"},
		{"TUIOS_SOCKET": "/tmp/s"},
		{"TMUX": "/tmp/t"},
		{"1BAD": "x"},
		{"A=B": "x"},
		{"NUL": "a\x00b"},
	} {
		if _, _, verr := callerEnv(nil, env); verr == nil || verr.Code != ErrVerbInvalidParams {
			t.Errorf("callerEnv(%v) = %v, want invalid_params", env, verr)
		}
	}
	for _, cs := range []*connState{{viaLink: true}, {paneOnly: true}} {
		if _, _, verr := callerEnv(cs, map[string]string{"PATH": "/usr/bin"}); verr == nil || verr.Code != ErrVerbForbidden {
			t.Errorf("env from another machine (%+v) = %v, want forbidden", cs, verr)
		}
	}
	got, path, verr := callerEnv(nil, map[string]string{"PATH": "/opt/bin:/usr/bin", "ANTHROPIC_MODEL": "o"})
	if verr != nil || path != "/opt/bin:/usr/bin" || strings.Join(got, ",") != "ANTHROPIC_MODEL=o,PATH=/opt/bin:/usr/bin" {
		t.Errorf("callerEnv = %q, %q, %v", got, path, verr)
	}
}

// TestBuildEnvWithPutsTheCallersVariablesUnderTuios: the caller's value
// replaces the daemon's, and cannot change what tuios sets after it.
func TestBuildEnvWithPutsTheCallersVariablesUnderTuios(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "env")
	t.Setenv("FAN_DAEMON_ONLY", "daemon")
	env := sess.buildEnvWith("w1", false, []string{"FAN_DAEMON_ONLY=caller", "TERM=dumb"})
	if got := envValue(env, "FAN_DAEMON_ONLY"); got != "caller" {
		t.Errorf("FAN_DAEMON_ONLY = %q, want the caller's", got)
	}
	count := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "FAN_DAEMON_ONLY=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("FAN_DAEMON_ONLY appears %d times", count)
	}
	// TERM is set after the caller's, so the last one, which exec keeps, is
	// tuios's.
	last := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			last = kv
		}
	}
	if last == "TERM=dumb" {
		t.Error("the caller's TERM won over the one tuios sets")
	}
}

// fakeProgram writes a script into a directory of its own and returns the
// directory. The directory is not on the daemon's PATH.
func fakeProgram(t *testing.T, name, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin-"+name)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// echoScript prints its arguments and a variable, then echoes what it reads.
const echoScript = "echo \"ARGS: $*\"\necho \"MARK: $FAN_MARK\"\nwhile IFS= read -r line; do echo \"GOT: $line\"; done\n"

// TestFanHoldsAPromptAndSaysSoInTheInbox: an agent that sits on a screen
// nothing recognises turns its prompt held and raises a question in the Inbox;
// once it is ready the prompt goes in and the question closes.
func TestFanHoldsAPromptAndSaysSoInTheInbox(t *testing.T) {
	old := agentHeldAfter
	agentHeldAfter = 150 * time.Millisecond
	t.Cleanup(func() { agentHeldAfter = old })

	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	c := dialVerb(t, sp)
	res := result(t, callVerb(t, c, "fan", map[string]any{"count": 1, "agent": "claude", "prompt": "Held task.", "repo": repo, "name": "held"}))
	row := res["sessions"].([]any)[0].(map[string]any)
	sess := d.manager.GetSession(row["session"].(string))
	windowID := row["window_id"].(string)
	// Claude Code can show idle, so unknown is not ready for it: this is the
	// first-run screen.
	if err := sess.SetDaemonWindowAgentState(windowID, AgentStateUnknown, ""); err != nil {
		t.Fatal(err)
	}
	info := waitPromptStatus(t, sess, PromptHeld)
	if !strings.Contains(info.PromptNote, "first-run") {
		t.Errorf("held note = %q", info.PromptNote)
	}
	items, _ := listAttention(t, c, `{"kinds":["question"]}`)
	if len(items) != 1 || items[0]["window"] != windowID || items[0]["summary"] != heldPromptSummary {
		t.Fatalf("the Inbox does not hold the question for the held prompt: %v", items)
	}

	if err := sess.SetDaemonWindowAgentState(windowID, AgentStateIdle, ""); err != nil {
		t.Fatal(err)
	}
	waitPromptStatus(t, sess, PromptSent)
	deadline := time.Now().Add(3 * time.Second)
	for {
		items, _ = listAttention(t, c, `{"kinds":["question"]}`)
		if len(items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the held question stayed after the prompt went in: %v", items)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestHeldQuestionLeavesThePanesOwnQuestionAlone: closing the held item does
// not close a real question the pane raised on the same key.
func TestHeldQuestionLeavesThePanesOwnQuestionAlone(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "held")
	c := dialVerb(t, sp)
	st := sess.GetState()
	w, _ := findWindowState(st, b)
	summary := d.attention.openHeldPrompt("held", w)
	setAgentState(t, c, "held", b, "needs_input", "question", "Which branch?")
	d.attention.closeHeldPrompt("held", b, summary)
	items, _ := listAttention(t, c, `{"kinds":["question"]}`)
	if len(items) != 1 || items[0]["summary"] != "Which branch?" {
		t.Errorf("closing the held item took the pane's own question with it: %v", items)
	}
}

func TestStartAgentStopsAtABlockedAgentAndAtNoEvidence(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "start2")
	claudeBin := fakeProgram(t, "claude", echoScript)
	toolBin := fakeProgram(t, "mytool", echoScript)
	c := dialVerb(t, sp)
	path := claudeBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + os.Getenv("PATH")

	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			for _, w := range sess.GetState().Windows {
				if w.CustomName == "trusting" {
					_ = sess.SetDaemonWindowAgentState(w.ID, AgentStateNeedsInput, "Do you trust this folder?")
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	res := result(t, callVerb(t, c, "start-agent", map[string]any{
		"session": "start2", "agent": "claude", "name": "trusting", "env": map[string]string{"PATH": path},
		"prompt": "x", "ready_timeout": 4000,
	}))
	if res["ready"] != false || res["outcome"] != string(agentStartBlocked) || res["prompt_status"] != PromptNotSent {
		t.Errorf("a blocked agent: %v, want ready false, outcome blocked and no prompt", res)
	}
	if _, ok := findWindowState(sess.GetState(), res["window_id"].(string)); !ok {
		t.Error("the blocked agent's pane was not kept")
	}

	// A program that never shows a state is never ready.
	raw, _ := json.Marshal(map[string]any{"session": "start2", "agent": "mytool", "env": map[string]string{"PATH": path}, "ready_timeout": 300})
	out, verr := d.verbStartAgent(nil, raw)
	if verr != nil {
		t.Fatal(verr)
	}
	res = out.(map[string]any)
	if res["ready"] != false || res["outcome"] != string(agentStartTimeout) {
		t.Errorf("a program with no evidence: %v, want ready false on timeout", res)
	}

	raw, _ = json.Marshal(map[string]any{"session": "start2", "agent": "claude", "env": map[string]string{"PATH": path}})
	if _, verr := d.verbStartAgent(&connState{viaLink: true}, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("start-agent with env over a link: %v, want forbidden", verr)
	}
}

// envValue returns the last value env holds for key, which is the one exec
// keeps.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range slices.Backward(env) {
		if len(kv) > len(prefix) && kv[:len(prefix)] == prefix {
			return kv[len(prefix):]
		}
	}
	return ""
}
