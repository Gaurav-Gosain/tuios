package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
//   - r on its rail row opens the reply editor, a paste of 42 lines becomes
//     one block, enter queues the reply as the person, and the row says
//     1 queued while the agent works;
//   - x on the row drops the reply and u a moment later queues it again;
//   - when the turn ends the reply is typed, and once the agent takes it the
//     queued figure goes. The pane is a recorder that keeps every byte it
//     reads, and it gets the typed words and all 42 lines of the paste,
//     line breaks and all, in one bracketed paste, then one Enter;
//   - after the person was away from the pane (agents.recap away = 1s) and a
//     turn finished, coming back to it puts the recap line in the dock.
//
// Negative control: with the reply keys answering "not handled", r on the row
// renames the pane and the wait for the editor times out; without the return
// hook in markFocusedAgentSeen, the wait for the recap line times out;
// with the reply paste flattened onto one line again (handler.go), the paste
// shows no block and the wait for it times out; with the block's label sent
// in place of its text, the recorder has no line 42;
// without the undo in the agent row's u, u marks the row unread and the wait
// for the queued figure after the undo times out.
func TestRichRowReplyAndRecap(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n\n[agents.recap]\naway = \"1s\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-rows", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	// The pane runs a recorder in place of the shell: bracketed paste on,
	// the terminal raw, and every byte it reads kept in a file.
	recorded := startRecorder(t, base, "e2e-rows")
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
	waitText(t, term, "the reply editor", "Reply to", "(working)", "Type your reply.", "new line")
	if err := term.SendKeys("echo r9c1e "); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed reply", "echo r9c1e")
	if err := term.Paste(replyPaste); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the paste as one block", "echo r9c1e [Pasted text #1 · 42 lines]", "ctrl+o open")
	if strings.Contains(term.Screen().Text(), "paste line 01") {
		t.Fatalf("the paste is drawn as text, not as a block\n%s", term.Snapshot())
	}
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
	got := waitRecorded(t, recorded, "\x1b[201~\r")
	want := "\x1b[200~echo r9c1e " + strings.TrimRight(replyPaste, "\n") + "\x1b[201~\r"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("the pane read %q\nwant it to end with %q", got, want)
	}
	if n := strings.Count(got, "paste line "); n != 42 {
		t.Fatalf("the pane read %d of the 42 pasted lines:\n%q", n, got)
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

// replyPaste is 42 lines, as a stack trace or a diff would be pasted.
var replyPaste = func() string {
	var b strings.Builder
	for i := 1; i <= 42; i++ {
		fmt.Fprintf(&b, "paste line %02d\tat main.go:%d\n", i, i*10)
	}
	return b.String()
}()

// recorderScript turns bracketed paste on, makes the terminal raw so no byte
// is changed or echoed, says it is ready, and keeps every byte it reads in
// the file it is given.
const recorderScript = `#!/bin/sh
stty raw -echo
printf '\033[?2004h'
printf 'recorder ready\r\n'
exec cat > "$1"
`

// startRecorder runs the recorder in the first pane of a session and returns
// the file it writes.
func startRecorder(t *testing.T, base, sessionName string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "recorder.sh")
	if err := os.WriteFile(script, []byte(recorderScript), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "recorded")
	if o, err := tuiosCLI(t, base, "send-keys", "-s", sessionName, "--raw", "exec sh "+script+" "+out); err != nil {
		t.Fatalf("start the recorder: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "send-keys", "-s", sessionName, "Enter"); err != nil {
		t.Fatalf("start the recorder: %v\n%s", err, o)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", sessionName)
		if strings.Contains(pane, "recorder ready") {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("the recorder never started:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitRecorded waits until the recorder's file holds end and returns it.
func waitRecorded(t *testing.T, file, end string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), end) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never read %q; it read %q", end, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
