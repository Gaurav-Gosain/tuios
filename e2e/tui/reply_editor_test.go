package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// How TestCockpitReplyEditorStdout could pass wrongly, written down first:
//   - The editor could be handed a pipe for stdout, as the scrollback editor
//     was: bubbletea gives a command the program's output, and the client's is
//     a wrapper, not a file. The fake editor records whether its stdout is a
//     terminal, and the test requires it.
//   - The fake editor could report a terminal wrongly. It tests [ -t 1 ] into
//     a variable before any redirect, since `if [ -t 1 ] ... fi > file` would
//     test the file.
//   - Ctrl+G could do something other than open the editor, and the record
//     would then never appear. The test waits for the record and fails on its
//     absence, not on a guess about the screen.
//   - replyEditorCmd has two callers, in inbox_reply.go and agent_mail.go.
//     This test reaches the one the agents view's reply box uses, and the
//     fix is in replyEditorCmd's command, which both share.

// fakeReplyEditor records whether its stdout is a terminal, then holds the
// terminal for a moment.
const fakeReplyEditor = `#!/bin/sh
if [ -t 1 ]; then out=terminal; else out=not-a-terminal; fi
printf '%s' "$out" > %DIR%/stdout
sleep 1
`

// TestCockpitReplyEditorStdout opens the reply box of an agent in the agents
// view and presses ctrl+g. The editor must get the terminal as its stdout.
// Then replySendNowOrQueue drives enter and alt+enter in the same box.
func TestCockpitReplyEditorStdout(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	rec := t.TempDir()
	editor := filepath.Join(t.TempDir(), "editor")
	script := []byte(strings.ReplaceAll(fakeReplyEditor, "%DIR%", rec))
	if err := os.WriteFile(editor, script, 0o755); err != nil {
		t.Fatal(err)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 55, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0", "EDITOR=" + editor, "VISUAL="}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.")
	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")

	if err := term.SendKeys(tuitest.Ctrl('g')); err != nil {
		t.Fatalf("send ctrl+g: %v", err)
	}
	var got []byte
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(rec, "stdout")); err == nil && len(b) > 0 {
			got = b
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == nil {
		t.Fatalf("ctrl+g in the reply box never ran the editor\n%s", term.Snapshot())
	}
	if string(got) != "terminal" {
		t.Errorf("the reply editor's stdout is %s, want the terminal: an editor on a pipe runs with echo on and prints the host's replies on the screen", got)
	}

	replySendNowOrQueue(t, term, base)
}

// replySendNowOrQueue is the send-now half of TestCockpitReplyEditorStdout.
// The reply box is open on docs-cli, which rests. The button reads Send and
// no send-now hint shows. The agent then reports working: the button reads
// Queue and the hints offer alt+enter to send now. enter queues a reply,
// which waits and is not typed. alt+enter on a second reply types it into the
// pane at once, while the agent still reports working, and the queue still
// holds only the first reply: send now leaves the queue as it is. Frames of
// the box at rest, at work, and after the send are saved under artifactDir.
//
// How it could pass wrongly, written down first:
//   - alt+enter could queue the reply, and the queue type it at a rest. The
//     agent never rests in this test, so the pane never shows it.
//   - The pane could show the text because the box echoed it, not because
//     it was typed. capture-pane reads the docs pane, which the client does
//     not show, and the shell there runs the line, so its output is there.
//   - The send-now reply could also join the queue and be typed twice. The
//     test reads the queue after the send.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with EventSendNow
// routed to InboxReplySend, the reply is queued, never reaches the pane, and
// the wait for it fails.
func replySendNowOrQueue(t *testing.T, term *tuitest.Terminal, base string) {
	const queued, now = "queued-reply-7d2e", "sent-now-4b9c"
	cli := func(what string, args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return out
	}
	waitText(t, term, "the reply box after the editor", "Type your reply.", "Send")
	if strings.Contains(term.Screen().Text(), "send now") {
		t.Errorf("ASSERTION: the box offers send now to an agent at rest, where enter sends at once\n%s", term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "reply-send-resting-200x55")

	cli("set-agent-state working", "set-agent-state", "working", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code")
	waitText(t, term, "the footer of a working agent", "Queue", "alt+↵ send now")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "reply-send-working-200x55")

	// enter queues while the agent works.
	if err := term.SendKeys("echo "+queued, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the closed box after enter", "Write a reply.")
	if out := cli("queue ls", "queue", "ls", "-s", "docs", "-w", "docs-cli"); !strings.Contains(out, "waiting") || !strings.Contains(out, "echo "+queued) {
		t.Fatalf("ASSERTION: enter did not queue the reply to a working agent: %q", out)
	}

	// alt+enter sends now.
	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")
	if err := term.SendKeys("echo " + now); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed reply", "echo "+now)
	if err := term.SendKeys(tuitest.Alt(tuitest.Enter)); err != nil {
		t.Fatal(err)
	}
	var pane string
	deadline := time.Now().Add(uiTimeout)
	for {
		pane = cli("capture-pane", "capture-pane", "-s", "docs", "-w", "docs-cli")
		// The command line and its output: the shell ran it.
		if strings.Count(pane, now) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: alt+enter did not type the reply into the working agent's pane\n%s\n%s", pane, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(pane, queued) {
		t.Errorf("ASSERTION: the queued reply was typed while the agent works\n%s", pane)
	}
	if out := cli("get-agent-state", "get-agent-state", "-s", "docs", "-w", "docs-cli", "--json"); !strings.Contains(strings.ReplaceAll(out, " ", ""), `"state":"working"`) {
		t.Errorf("the agent left working before the check, so the send was not proved to be at work: %s", out)
	}
	out := cli("queue ls", "queue", "ls", "-s", "docs", "-w", "docs-cli")
	if strings.Contains(out, now) || !strings.Contains(out, "echo "+queued) {
		t.Errorf("ASSERTION: send now changed the queue: %q, want only the queued reply", out)
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "reply-send-now-done-200x55")
	alive(t, term, "after send now")
}
