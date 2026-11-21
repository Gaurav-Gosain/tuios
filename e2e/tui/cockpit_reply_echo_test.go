package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitReplyEchoes sends replies from the agents view's reply area and
// watches the thread show them before the transcript does: queued, typed,
// refused and cancelled, and queued in a client opened later.
//
// The ways it can go wrong, each an assertion here:
//   - the sent reply shows nowhere until the transcript has it (the person's
//     report);
//   - it shows twice once the transcript entry arrives;
//   - it goes too early, or an older prompt with the same words takes it;
//   - a queued reply is lost from the thread in a client opened again;
//   - a refused reply shows nothing, or "Sending" for ever, and retry
//     queues nothing;
//   - cancel takes the echo away and leaves the message queued.
//
// The agent is a Claude Code pane whose transcript the test writes, as the
// harness would. [agents.queue] max = 1 makes the second queued reply a
// refusal. Frames before and after each step go under artifactDir.
func TestCockpitReplyEchoes(t *testing.T) {
	base, path := echoThreadBase(t, "[agents.queue]\nmax = 1\n")
	term := startEchoClient(t, base)
	saveCockpitFrames(t, term, "echo-0-before")

	sendReply(t, term, "Use the grid gap instead.")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Use the grid gap instead.", "Queued, typed when it rests", "cancel")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "echo-1-missing")
		t.Fatalf("ASSERTION: the queued reply does not show in the thread as queued: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "echo-1-queued")

	sendReply(t, term, "And check the padding.")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "And check the padding.", "Not sent:", "retry")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "echo-2-missing")
		t.Fatalf("ASSERTION: the refused reply does not show as not sent with retry: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "echo-2-refused")

	row, col := findText(t, term, "cancel")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Use the grid gap instead.") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: cancel did not take the queued reply away: %v\n%s", err, term.Snapshot())
	}
	if out, _ := tuiosCLI(t, base, "queue", "ls", "-s", "api"); strings.Contains(out, "Use the grid gap") {
		t.Fatalf("ASSERTION: the cancelled reply is still in the daemon's queue:\n%s", out)
	}
	saveCockpitFrames(t, term, "echo-3-cancelled")

	row, col = findText(t, term, "retry")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return containsAll(txt, "And check the padding.", "Queued, typed when it rests") && !strings.Contains(txt, "Not sent:")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: retry did not queue the refused reply: %v\n%s", err, term.Snapshot())
	}
	if out, _ := tuiosCLI(t, base, "queue", "ls", "-s", "api"); strings.Count(out, "And check the padding") != 1 {
		t.Fatalf("ASSERTION: the retried reply is not in the daemon's queue once:\n%s", out)
	}
	saveCockpitFrames(t, term, "echo-4-retried")

	_ = term.Close()
	term = startEchoClient(t, base)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "And check the padding.", "Queued, typed when it rests")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "echo-5-missing")
		t.Fatalf("ASSERTION: a new client does not show the queued reply: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "echo-5-reopened")

	if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", "api", "-w", "header-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Sent, waiting for") && !strings.Contains(s.Text(), "Queued, typed when it rests")
	}, 3*uiTimeout); err != nil {
		saveCockpitFrames(t, term, "echo-6-missing")
		out, _ := tuiosCLI(t, base, "queue", "ls", "-s", "api")
		t.Fatalf("ASSERTION: the typed reply does not show as sent: %v\nqueue: %s\n%s", err, out, term.Snapshot())
	}
	saveCockpitFrames(t, term, "echo-6-typed")
	appendTranscript(t, path, map[string]any{"type": "user", "timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message": map[string]any{"role": "user", "content": "And check the padding."}, "uuid": "u-echo", "isSidechain": false})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Count(txt, "And check the padding.") == 1 && !strings.Contains(txt, "Sent, waiting for")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "echo-7-twice")
		t.Fatalf("ASSERTION: once the transcript has the reply, it does not show once, as the transcript's: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "echo-7-landed")
	alive(t, term, "after the reply echoes")
}

func echoThreadBase(t *testing.T, extra string) (string, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n"+extra)
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	path := filepath.Join(atProjects(base), "work", "e3c0ff1e.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	at := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, path, map[string]any{"type": "user", "timestamp": at(3 * time.Minute), "uuid": "u0", "isSidechain": false,
		"message": map[string]any{"role": "user", "content": "Fix the header padding."}})
	appendTranscript(t, path, map[string]any{"type": "assistant", "timestamp": at(2 * time.Minute), "uuid": "u1", "isSidechain": false,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Looking at the header now."}}}})
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "header-fix"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "header-fix")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "e3c0ff1e"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "api", "-w", "header-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	return base, path
}

func startEchoClient(t *testing.T, base string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Fix the header padding.", "Looking at the header now.")
	_ = term.WaitStable(uiTimeout)
	return term
}

func sendReply(t *testing.T, term *tuitest.Terminal, text string) {
	t.Helper()
	if err := term.SendKeys("r"); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if err := term.SendKeys(text); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
}

func appendTranscript(t *testing.T, path string, rec map[string]any) {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n", b); err != nil {
		t.Fatal(err)
	}
}
