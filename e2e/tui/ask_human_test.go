package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// startAskHuman runs `tuios ask-human` in the background against the suite's
// daemon and returns a channel that delivers its stdout and exit error.
func startAskHuman(t *testing.T, base string, args ...string) <-chan askRun {
	t.Helper()
	return startCLIBackground(t, base, append([]string{"ask-human"}, args...)...)
}

// startCLIBackground runs a tuios command in the background against the
// suite's daemon and returns a channel that delivers its stdout and exit
// error, for a command that blocks until something happens on the screen.
func startCLIBackground(t *testing.T, base string, args ...string) <-chan askRun {
	t.Helper()
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", args, err)
	}
	done := make(chan askRun, 1)
	finished := make(chan struct{})
	go func() {
		err := cmd.Wait()
		done <- askRun{out: stdout.String(), err: err}
		close(finished)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-finished
	})
	return done
}

// askRun is how a background ask-human ended.
type askRun struct {
	out string
	err error
}

// TestAskHumanPopsUpOnThePaneInFront is the ask-human round trip against a
// real daemon and a real client. A question about the pane the person is
// looking at opens the Inbox on it with no key pressed, the digit picks the
// answer once it has been on screen, and the waiting command prints the
// answer and exits 0.
//
// Negative control: with inboxPopAsk returning false the Inbox never opens
// and the first wait fails; with the digit keys still bound only to the
// approval decisions, the command never returns.
func TestAskHumanPopsUpOnThePaneInFront(t *testing.T) {
	term, base := attachClientBase(t)

	asked := startAskHuman(t, base, "-s", "e2e-ctrlp", "-w", "0", "--timeout", "60000",
		"Deploy to staging?", "-o", "yes", "-o", "no")

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Asked you 1", "[1-2] Deploy to staging?", "2  no", "answer")
	}, uiTimeout); err != nil {
		t.Fatalf("the question never came up on the client showing the pane: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "ask-human-popup")
	select {
	case run := <-asked:
		t.Fatalf("ask-human returned before anyone answered: %q, %v", run.out, run.err)
	default:
	}
	time.Sleep(answerSettle)

	if err := term.SendKeys("2"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	select {
	case run := <-asked:
		if run.err != nil || strings.TrimSpace(run.out) != "no" {
			t.Fatalf("ask-human printed %q and ended with %v, want the answer no and exit 0", run.out, run.err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("ask-human never returned after the answer\n%s", term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Deploy to staging?")
	}, uiTimeout); err != nil {
		t.Fatalf("the answered question stayed in the Inbox: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after answering a question")
}

// TestAskHumanPopIgnoresKeysTypedForThePane presses d the moment the question
// pops, as a person typing to the agent would. The key does not dismiss the
// question: it stays on screen and the waiting command keeps waiting. Once the
// question has been on screen for a moment, a digit answers it.
//
// Negative control: without the settle check in handleInboxInput, d dismisses
// the question and ask-human exits 1 at once.
func TestAskHumanPopIgnoresKeysTypedForThePane(t *testing.T) {
	term, base := attachClientBase(t)

	asked := startAskHuman(t, base, "-s", "e2e-ctrlp", "-w", "0", "--timeout", "60000",
		"Squash the commits?", "-o", "yes", "-o", "no")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Asked you 1", "[1-2] Squash the commits?")
	}, uiTimeout); err != nil {
		t.Fatalf("the question never came up on the client showing the pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("d"); err != nil {
		t.Fatalf("press d: %v", err)
	}
	time.Sleep(answerSettle)
	if !strings.Contains(term.Snapshot(), "Squash the commits?") {
		t.Fatalf("d pressed as the question appeared dismissed it\n%s", term.Snapshot())
	}
	select {
	case run := <-asked:
		t.Fatalf("ask-human returned after a key typed as the question appeared: %q, %v", run.out, run.err)
	default:
	}

	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	select {
	case run := <-asked:
		if run.err != nil || strings.TrimSpace(run.out) != "yes" {
			t.Fatalf("ask-human printed %q and ended with %v, want the answer yes and exit 0", run.out, run.err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("ask-human never returned after the answer\n%s", term.Snapshot())
	}
	alive(t, term, "after a key typed as a question popped")
}

// TestAskHumanAnswerReachesAKilledCaller kills the waiting ask-human, as a
// harness does to a command past its time limit, and then answers from the
// Inbox. The answer is mailed to the asking pane from human, verified.
//
// Negative control: without the replyFailed hook the answer goes to the dead
// call and the pane's inbox stays empty.
func TestAskHumanAnswerReachesAKilledCaller(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-killed", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	cmd := exec.Command(tuiosBin, "ask-human", "-s", "e2e-killed", "-w", "0", "--timeout", "60000", "Tag the release?", "-o", "tag", "-o", "skip")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ask-human: %v", err)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Asked you 1", "Tag the release?")
	}, uiTimeout); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("the Inbox never listed the question: %v\n%s", err, term.Snapshot())
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	time.Sleep(answerSettle)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		mail, err := tuiosCLI(t, base, "read-agent-messages", "-s", "e2e-killed", "-w", "0", "--json")
		if err == nil && strings.Contains(mail, `"text": "tag"`) && strings.Contains(mail, `"verified_human": true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the killed caller's pane inbox = %q (%v), want the answer tag from human, verified\n%s", mail, err, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after answering a killed caller's question")
}

// TestPopupWaitReturnsWhatThePickerPrinted is popup --capture-stdout on a real
// client: the picker draws its prompt in the popup, the person types the
// answer there, and the command that opened the popup prints the answer and
// nothing the picker drew.
//
// Negative control: without capture_stdout the answer goes to the popup's own
// screen, the command prints nothing, and the last check fails.
func TestPopupWaitReturnsWhatThePickerPrinted(t *testing.T) {
	term, base := attachClientBase(t)
	picked := startCLIBackground(t, base, "popup", "-s", "e2e-ctrlp", "--capture-stdout", "--timeout", "60000",
		"--", "sh", "-c", `printf 'PICK-ONE> ' >&2; read x; echo "$x"`)

	if err := term.WaitForText("PICK-ONE>", uiTimeout); err != nil {
		t.Fatalf("the picker never drew its prompt in the popup: %v\n%s", err, term.Snapshot())
	}
	select {
	case run := <-picked:
		t.Fatalf("popup --capture-stdout returned before the picker did: %q, %v", run.out, run.err)
	default:
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "beta\r"); err != nil {
		t.Fatalf("type into the popup: %v\n%s", err, out)
	}
	select {
	case run := <-picked:
		if run.err != nil || run.out != "beta\n" {
			t.Fatalf("popup --capture-stdout printed %q and ended with %v, want %q and exit 0", run.out, run.err, "beta\n")
		}
	case <-time.After(uiTimeout):
		t.Fatalf("popup --capture-stdout never returned\n%s", term.Snapshot())
	}
	alive(t, term, "after a captured popup closed")
}

// TestAskHumanWaitsInTheInboxWhenNobodyIsThere covers the fallback: the
// question is asked in a session nobody is attached to, the call returns
// pending, and the person answers it from the Inbox of another session later.
// The answer is then in the asking pane's inbox, from human and verified, and
// coming back with the request id reads it.
func TestAskHumanWaitsInTheInboxWhenNobodyIsThere(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	out, _ := tuiosCLI(t, base, "ask-human", "-s", "e2e-agent", "-w", "0", "--no-wait", "--json",
		"Merge the branch?", "-o", "merge", "-o", "wait")
	var res struct {
		Result struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		} `json:"result"`
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		t.Fatalf("ask-human --json printed %q: %v", out, err)
	}
	id, status := res.RequestID, res.Status
	if id == "" {
		id, status = res.Result.RequestID, res.Result.Status
	}
	if status != "pending" || id == "" {
		t.Fatalf("ask-human --no-wait = %q, want pending with a request id", out)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Asked you 1", "Merge the branch?")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the question from the detached session: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "ask-human-inbox")
	time.Sleep(answerSettle)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Merge the branch?")
	}, uiTimeout); err != nil {
		t.Fatalf("the answered question stayed in the Inbox: %v\n%s", err, term.Snapshot())
	}

	mail, err := tuiosCLI(t, base, "read-agent-messages", "-s", "e2e-agent", "-w", "0", "--json")
	if err != nil || !strings.Contains(mail, `"text": "merge"`) || !strings.Contains(mail, `"verified_human": true`) {
		t.Fatalf("the asking pane's inbox = %q (%v), want the answer merge from human, verified", mail, err)
	}
	again, err := tuiosCLI(t, base, "ask-human", "--request-id", id)
	if err != nil || strings.TrimSpace(again) != "merge" {
		t.Fatalf("coming back for the answer printed %q (%v), want merge", again, err)
	}
	alive(t, term, "after answering a question from the Inbox")
}
