package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
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
// Negative control: with imeCaret skipping the open draft (cursor_park.go),
// the host cursor wait in the reply editor times out. With the reply keys
// answering "not handled", r on the row
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
	waitText(t, term, "the reply editor", "Reply to", "typed when it rests", "Type your reply.", "new line")
	if err := term.SendKeys("echo r9c1e "); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed reply", "echo r9c1e")
	// The editor draws its own caret, so the host cursor is hidden. It is
	// parked on that caret, the cell after the typed space, because an input
	// method opens its candidate window at the host cursor (#649).
	if col, row, ok := textCell(term.Screen(), "echo r9c1e"); !ok {
		t.Fatalf("the typed reply is not on the screen\n%s", term.Snapshot())
	} else {
		waitHostCursor(t, term, col+len("echo r9c1e "), row, false, "in the reply editor")
	}
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

// TestReplyWithALargePasteGoesAsAFile: a reply whose paste takes it past
// 12 KiB reaches the agent with the paste as a file. The pane reads the typed
// words and the label that names the file, and never the paste itself; the
// file, in the daemon's paste folder, holds every byte of the paste.
//
// Negative control: with every block typed in its place (composeQueued), the
// recorder reads the paste and has no label, and the wait for the label
// times out.
func TestReplyWithALargePasteGoesAsAFile(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-big", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	recorded := startRecorder(t, base, "e2e-big")
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-big"}})
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
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-big", "--window", "0", "--timeout", "10s")
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
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-big","prompt":"find the flaky test"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-big","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	waitText(t, term, "the working row", "Bash: go test")

	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("G", "k", "k", "r"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the reply editor", "Reply to", "Type your reply.")
	if err := term.SendKeys("echo b1g9 "); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed reply", "echo b1g9")
	big := largeReplyPaste()
	if err := term.Paste(big); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the large paste as one block", "[Pasted text #1 · 2,048 lines]")
	saveFrame(t, term, "inbox-reply-large-paste")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the queued figure on the row", "1 queued")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	// The turn ends and the reply is typed at the rest.
	hook(`{"hook_event_name":"Stop","session_id":"e2e-big","last_assistant_message":"Found it."}`)
	got := waitRecorded(t, recorded, "lines]\x1b[201~\r")
	start := strings.LastIndex(got, "\x1b[200~")
	typed := got[start+len("\x1b[200~") : len(got)-len("\x1b[201~\r")]
	if !strings.HasPrefix(typed, "echo b1g9 [Pasted text #1: ") || !strings.HasSuffix(typed, ", 2,048 lines]") {
		t.Fatalf("the pane read %q, want the words and the label that names the file", typed)
	}
	if strings.Contains(got, "big paste line") {
		t.Fatal("the paste itself was typed into the pane")
	}
	path := strings.TrimSuffix(strings.TrimPrefix(typed, "echo b1g9 [Pasted text #1: "), ", 2,048 lines]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the file the label names: %v", err)
	}
	if string(data) != big {
		t.Fatalf("the file holds %d bytes, want the %d of the paste", len(data), len(big))
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 || filepath.Base(filepath.Dir(path)) != "paste" {
		t.Fatalf("the file is %s (%v), want mode 0600 in the paste folder", path, err)
	}
	alive(t, term, "after the large reply")
}

// largeReplyPaste is 2,048 lines, about 100 KiB, as a long log is pasted.
func largeReplyPaste() string {
	var b strings.Builder
	for i := 1; i <= 2048; i++ {
		fmt.Fprintf(&b, "big paste line %04d of the failing run, at main.go:%d\n", i, i)
	}
	return b.String()
}

// TestReplyWithAnImageGoesAsAFile: a path to an image file pasted into the
// reply, as a terminal pastes a dropped file, attaches the image as a block.
// The agent is Claude Code, whose manifest takes an image as a path in a
// paste of its own. The recorder pane reads the typed words in one paste,
// the image's path alone in the next, and one Enter. The file at that path
// holds the image's bytes.
//
// Negative control: with the path pasted as text (no image read in
// inboxReplyPasteCmd), the block never shows and the wait for it times out.
func TestReplyWithAnImageGoesAsAFile(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-img", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	recorded := startRecorder(t, base, "e2e-img")
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-img"}})
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
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-img", "--window", "0", "--timeout", "10s")
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
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"e2e-img","prompt":"fix the layout"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"e2e-img","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	waitText(t, term, "the working row", "Bash: go test")

	shot := filepath.Join(t.TempDir(), "screen shot.png")
	pngBytes := tinyPNG(t, 64, 48)
	if err := os.WriteFile(shot, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("G", "k", "k", "r"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the reply editor", "Reply to", "Type your reply.", "ctrl+v paste image")
	if err := term.SendKeys("echo i9m6 "); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed reply", "echo i9m6")
	// A dropped file arrives as its path, with the space escaped.
	if err := term.Paste(strings.ReplaceAll(shot, " ", `\ `)); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the image as one block", "echo i9m6 [Image #1 · 64×48 png]")
	saveFrame(t, term, "inbox-reply-image")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the queued figure on the row", "1 queued")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	hook(`{"hook_event_name":"Stop","session_id":"e2e-img","last_assistant_message":"Done."}`)
	got := waitRecorded(t, recorded, "\x1b[201~\r")
	words := "\x1b[200~echo i9m6 \x1b[201~"
	at := strings.LastIndex(got, words)
	if at < 0 {
		t.Fatalf("the pane read %q, want the words in a paste of their own", got)
	}
	rest := got[at+len(words):]
	if !strings.HasPrefix(rest, "\x1b[200~") || !strings.HasSuffix(rest, ".png\x1b[201~\r") {
		t.Fatalf("after the words the pane read %q, want the image's path alone in a paste, then Enter", rest)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(rest, "\x1b[200~"), "\x1b[201~\r")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(pngBytes) {
		t.Fatalf("the file %s holds %d bytes (%v), want the image's %d", path, len(data), err, len(pngBytes))
	}
	if filepath.Base(filepath.Dir(path)) != "paste" {
		t.Fatalf("the image went to %s, want the paste folder", path)
	}
	alive(t, term, "after the reply with an image")
}

// tinyPNG is a w by h PNG.
func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestReplyToAnAgentOnAHost: a reply for an agent on a linked machine goes to
// this machine's daemon with host, which passes it over the link. The far
// daemon queues it as the link's, writes the large paste to a file on its own
// machine, and types the reply into the pane there when the agent comes to
// rest. The pane is a recorder; it reads the typed words and the label that
// names the file, and the file holds the paste.
//
// Negative control: with host left out of queuePromptParams (the forward
// gone), this daemon looks for the far session here and the call fails
// with session_not_found.
func TestReplyToAnAgentOnAHost(t *testing.T) {
	f := startGlobalFleet(t)
	recorded := startRecorderWithin(t, f.alpha, f.env, "far")
	if out, err := cliWithin(t, f.alpha, f.env, "set-agent-state", "working", "-s", "far", "--harness", "claude-code"); err != nil {
		t.Fatalf("mark the far pane an agent: %v\n%s", err, out)
	}
	var window string
	for id := range sessionWindowHosts(t, f.alpha, f.env, "far") {
		window = id
	}
	big := largeReplyPaste()
	params, _ := json.Marshal(map[string]any{
		"host": "alpha", "session": "far", "window": window,
		"parts": []map[string]any{
			{"kind": "text", "text": "echo h0st "},
			{"kind": "paste", "text": big, "n": 1},
		},
	})
	out, err := cliWithin(t, f.here, f.env, "call", "queue-prompt", string(params))
	if err != nil || !strings.Contains(out, `"host":"alpha"`) {
		t.Fatalf("queue-prompt with host: %v\n%s", err, out)
	}
	ls, _ := cliWithin(t, f.alpha, f.env, "queue", "ls", "-s", "far")
	if !strings.Contains(ls, "link:") {
		t.Fatalf("the far queue does not hold the link's entry:\n%s", ls)
	}
	if out, err := cliWithin(t, f.alpha, f.env, "set-agent-state", "idle", "-s", "far", "--harness", "claude-code"); err != nil {
		t.Fatalf("rest the far agent: %v\n%s", err, out)
	}
	got := waitRecorded(t, recorded, "lines]\x1b[201~\r")
	start := strings.LastIndex(got, "\x1b[200~")
	typed := got[start+len("\x1b[200~") : len(got)-len("\x1b[201~\r")]
	if !strings.HasPrefix(typed, "echo h0st [Pasted text #1: ") {
		t.Fatalf("the far pane read %q, want the words and the label that names the file", typed)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(typed, "echo h0st [Pasted text #1: "), ", 2,048 lines]")
	if farRuntime := xdgDir(f.alpha, "XDG_RUNTIME_DIR"); !strings.HasPrefix(path, farRuntime) {
		t.Fatalf("the file %s is not in the far machine's runtime folder %s", path, farRuntime)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != big {
		t.Fatalf("the far file holds %d bytes (%v), want the %d of the paste", len(data), err, len(big))
	}

	// alpha takes files away from the link. A reply with a small paste goes
	// as text, the paste typed in its place, since no file can be written
	// there. Negative control: without the retry as text in
	// queuePromptToHost, the call fails as forbidden.
	writeRemoteConfig(t, f.alpha, relayNoFilesPolicy)
	if out, err := cliWithin(t, f.alpha, f.env, "config", "apply"); err != nil {
		t.Fatalf("apply alpha's config: %v\n%s", err, out)
	}
	if out, err := cliWithin(t, f.alpha, f.env, "set-agent-state", "working", "-s", "far", "--harness", "claude-code"); err != nil {
		t.Fatalf("mark the far agent working: %v\n%s", err, out)
	}
	small := "small paste line 1\nsmall paste line 2\nsmall paste line 3\nsmall paste line 4\n"
	params, _ = json.Marshal(map[string]any{
		"host": "alpha", "session": "far", "window": window,
		"text": "echo n0f " + small,
		"parts": []map[string]any{
			{"kind": "text", "text": "echo n0f "},
			{"kind": "paste", "text": small, "n": 1},
		},
	})
	out, err = cliWithin(t, f.here, f.env, "call", "queue-prompt", string(params))
	if err != nil || !strings.Contains(out, `"host":"alpha"`) {
		t.Fatalf("queue-prompt with host to a link without files: %v\n%s", err, out)
	}
	if out, err := cliWithin(t, f.alpha, f.env, "set-agent-state", "idle", "-s", "far", "--harness", "claude-code"); err != nil {
		t.Fatalf("rest the far agent: %v\n%s", err, out)
	}
	got = waitRecorded(t, recorded, "line 4\x1b[201~\r")
	if !strings.Contains(got, "\x1b[200~echo n0f "+strings.TrimSuffix(small, "\n")+"\x1b[201~\r") {
		t.Fatalf("the far pane read %q, want the words and the paste in one paste", got[strings.LastIndex(got, "\x1b[200~"):])
	}
}

// startRecorderWithin is startRecorder for the daemon at base with env.
func startRecorderWithin(t *testing.T, base string, env []string, sessionName string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "recorder.sh")
	if err := os.WriteFile(script, []byte(recorderScript), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "recorded")
	if o, err := cliWithin(t, base, env, "send-keys", "-s", sessionName, "--raw", "exec sh "+script+" "+out); err != nil {
		t.Fatalf("start the recorder: %v\n%s", err, o)
	}
	if o, err := cliWithin(t, base, env, "send-keys", "-s", sessionName, "Enter"); err != nil {
		t.Fatalf("start the recorder: %v\n%s", err, o)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		pane, _ := cliWithin(t, base, env, "capture-pane", "-s", sessionName)
		if strings.Contains(pane, "recorder ready") {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("the recorder never started:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
