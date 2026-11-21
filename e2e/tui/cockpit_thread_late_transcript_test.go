package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadReadsATranscriptThatCameLate keeps the agents view open on
// a Claude Code agent that has no transcript yet, as a fresh claude has none
// until its first prompt, and sends it a reply. No key is pressed after that.
//
// The ways it can go wrong, each an assertion here:
//   - the thread asks for the transcript once, is told there is none, and
//     never asks again while the view stays open: the conversation shows only
//     after a close and reopen (the audit's P0-1);
//   - the reply keeps "Sent, waiting for" after the agent took it and
//     finished;
//   - the agent's answer ("Says: ...") is drawn above the prompt it answers.
//
// The agent's states and its transcript are written by the test, as the
// harness would write them. Frames of each step go under artifactDir.
func TestCockpitThreadReadsATranscriptThatCameLate(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	cli := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	cli("set-window", "-s", "api", "-w", "0", "--name", "fresh")
	cli("set-agent-state", "idle", "-s", "api", "-w", "fresh", "--harness", "claude-code", "-m", "Ready for a task.")

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the agent's row and its fallback thread", "fresh", "Ready for a task.")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "late-0-no-transcript")

	const prompt = "Add a memo cache to fib."
	sendReply(t, term, prompt)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Sent, waiting for") }, 3*uiTimeout); err != nil {
		saveCockpitFrames(t, term, "late-1-missing")
		t.Fatalf("ASSERTION: the reply does not show as sent: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "late-1-sent")

	cli("set-agent-state", "working", "-s", "api", "-w", "fresh", "--harness", "claude-code")
	cli("set-agent-state", "idle", "-s", "api", "-w", "fresh", "--harness", "claude-code", "-m", "Memo cache added.")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "Memo cache added.") && !strings.Contains(txt, "Sent, waiting for")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "late-2-stale")
		t.Fatalf("ASSERTION: after the agent took the reply and finished, the thread still says it waits: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "late-2-answered")
	promptRow, saysRow := -1, -1
	for i, line := range strings.Split(term.Screen().Text(), "\n") {
		if strings.Contains(line, prompt) && promptRow < 0 {
			promptRow = i
		}
		if strings.Contains(line, "Memo cache added.") {
			saysRow = i
		}
	}
	if promptRow < 0 || saysRow < promptRow {
		t.Fatalf("ASSERTION: the answer (row %d) is drawn above the prompt it answers (row %d):\n%s", saysRow, promptRow, term.Snapshot())
	}

	path := filepath.Join(atProjects(base), "work", "late0001.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	appendTranscript(t, path, map[string]any{"type": "user", "timestamp": now.Format("2006-01-02T15:04:05.000Z"), "uuid": "late-u0", "isSidechain": false,
		"message": map[string]any{"role": "user", "content": prompt}})
	appendTranscript(t, path, map[string]any{"type": "assistant", "timestamp": now.Add(time.Second).Format("2006-01-02T15:04:05.000Z"), "uuid": "late-u1", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "The fib cache is in place and the tests pass."}}}})
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "fresh")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "late0001"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	cli("set-agent-state", "working", "-s", "api", "-w", "fresh", "--harness", "claude-code")
	cli("set-agent-state", "idle", "-s", "api", "-w", "fresh", "--harness", "claude-code", "-m", "Memo cache added.")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "The fib cache is in place and the tests pass.") && strings.Count(txt, prompt) == 1
	}, 3*uiTimeout); err != nil {
		saveCockpitFrames(t, term, "late-3-missing")
		_ = term.SendKeys(tuitest.Esc)
		_ = term.WaitStable(uiTimeout)
		_ = term.SendKeys(tuitest.Ctrl('b'), "i")
		reopened := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "The fib cache is in place and the tests pass.")
		}, uiTimeout) == nil
		t.Fatalf("ASSERTION: the thread did not read the transcript that came while it was open (a view opened again reads it: %v): %v\n%s", reopened, err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "late-3-transcript")
	alive(t, term, "after the late transcript")
}
