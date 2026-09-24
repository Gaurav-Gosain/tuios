package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRichRowReplyAndRecap drives the rich agent row, the reply editor and the
// away recap end to end, with Claude Code hook payloads through the real
// `tuios agent-hook`:
//
//   - a working agent's rail row says what it is doing now (Bash: go test);
//   - r on its rail row opens the reply editor, enter queues the reply as the
//     person, and the row says 1 queued while the agent works;
//   - x on the row drops the reply and u a moment later queues it again;
//   - when the turn ends the reply is typed, and once the agent takes it the
//     queued figure goes;
//   - after the person was away from the pane (agents.recap away = 1s) and a
//     turn finished, coming back to it puts the recap line in the dock.
//
// Negative control: with the reply keys answering "not handled", r on the row
// renames the pane and the wait for the editor times out; without the return
// hook in markFocusedAgentSeen, the wait for the recap line times out;
// without the undo in the agent row's u, u marks the row unread and the wait
// for the queued figure after the undo times out.
func TestRichRowReplyAndRecap(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n\n[agents.recap]\naway = \"1s\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-rows", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-rows"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "window management mode", "Window management mode")
	time.Sleep(insertGuard)

	hook := func(payload string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-rows", "--window", "0", "--timeout", "10s")
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("the hook for %s failed or printed: %v\n%s", payload, err, out)
		}
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-rows","prompt":"make the retry configurable"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-rows","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	waitText(t, term, "the working row saying what it does", "Bash: go test")
	saveFrame(t, term, "rail-row-now")

	// The rail takes the keyboard; the agent row is two up from the last
	// row, as in the rail unread test. r opens the reply editor.
	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("G", "k", "k", "r"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the reply editor", "Reply to", "(working): _", "send when ready")
	if err := term.SendKeys("echo r9c1e"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed reply", "echo r9c1e_")
	saveFrame(t, term, "inbox-reply-editor")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the queued figure on the row", "1 queued")
	saveFrame(t, term, "rail-row-queued")
	out, err := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-rows")
	if err != nil || !strings.Contains(out, "human") || !strings.Contains(out, "echo r9c1e") {
		t.Fatalf("tuios queue ls = %q (%v), want the person's reply waiting", out, err)
	}

	// x on the same row drops the reply, and u a moment later queues it
	// again, as the person.
	if err := term.SendKeys("x"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the drop in the dock", "Dropped 1 queued message", "u undoes")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "1 queued") }, uiTimeout); err != nil {
		t.Fatalf("the queued figure stayed after x dropped the reply: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-rows"); err != nil || strings.Contains(out, "echo r9c1e") {
		t.Fatalf("tuios queue ls = %q (%v) after x, want the reply gone", out, err)
	}
	if err := term.SendKeys("u"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the queued figure after the undo", "1 queued")
	out, err = tuiosCLI(t, base, "queue", "ls", "-s", "e2e-rows")
	if err != nil || !strings.Contains(out, "human") || !strings.Contains(out, "echo r9c1e") {
		t.Fatalf("tuios queue ls = %q (%v) after u, want the reply waiting again as the person", out, err)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)

	// A second window takes the focus, so the person is away from the
	// agent's pane from here.
	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("no second window: %v\n%s", err, term.Snapshot())
	}

	// The turn ends: the reply is typed at the rest, and the agent taking
	// it (its next prompt) clears the queue and the figure.
	hook(`{"hook_event_name":"PostToolUse","session_id":"e2e-rows","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stdout":"ok"}}`)
	hook(`{"hook_event_name":"Stop","session_id":"e2e-rows","last_assistant_message":"Added retry with backoff."}`)
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "queue", "ls", "-s", "e2e-rows")
		if !strings.Contains(out, "waiting") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reply was never typed at rest:\n%s\n%s", out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-rows","prompt":"echo r9c1e"}`)
	hook(`{"hook_event_name":"Stop","session_id":"e2e-rows","last_assistant_message":"Printed the reply."}`)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "1 queued") }, uiTimeout); err != nil {
		t.Fatalf("the queued figure stayed after the reply was taken: %v\n%s", err, term.Snapshot())
	}

	// Away for more than a second with turns finished: going back to the
	// pane says what it did.
	time.Sleep(1200 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('b'), "p"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the away recap in the dock", "while you were away", "turns")
	saveFrame(t, term, "dock-away-recap")
	alive(t, term, "after the row, the reply and the recap")
}
