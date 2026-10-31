package tuie2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestQuietPaneKeepsOutOfTheInbox drives a real pane through turns, with the
// reports typed in the pane's own shell. A quiet pane opens no Inbox item and
// raises no alert, and its agent-state events still arrive. The person
// marks it, the pane cannot, and an approval still gets through.
//
// How this could pass wrongly, written down first:
//   - The Inbox could be empty because nothing was reported. A loud turn comes
//     first and must open its item, and the event stream must carry the quiet one.
//   - An item could arrive after the check. Each check runs after the event for
//     that report has come back, and the event is sent after the Inbox is told.
//   - No alert could fire here at all. The loud turn and the approval must raise
//     theirs.
//
// Negative controls in NEGATIVE_CONTROLS.md.
func TestQuietPaneKeepsOutOfTheInbox(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[agents.permissions]
mode = "strict"
grants = ["read"]

[notifications.agent]
settle_seconds = 0
suppress_focused = false

[notifications.agent.states]
done = true
needs_input = true
errored = true
`)
	killDaemon(t, base)
	host := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, out: host, env: copyColorOpts.env, args: []string{"new", "e2e-quiet"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")
	renameWindow(t, term, "WORKER")
	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("sidebar did not open: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)

	// The subscription prints its ack first, so an event cannot come before it.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sub := exec.CommandContext(ctx, tuiosBin, "subscribe", "-s", "e2e-quiet", "--types", "agent-state")
	sub.Dir = workDirIn(t, base)
	sub.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		sub.Env = append(sub.Env, key+"="+xdgDir(base, key))
	}
	pipe, err := sub.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = sub.Wait() })
	events := make(chan string, 64)
	go func() {
		defer close(events)
		lines := bufio.NewScanner(pipe)
		for lines.Scan() {
			events <- lines.Text()
		}
		// The wait for an event says the stream ended, and this says why.
		if err := lines.Err(); err != nil {
			events <- "the event stream failed: " + err.Error()
		}
	}()
	waitEvent := func(want string) {
		t.Helper()
		deadline := time.After(uiTimeout)
		for {
			select {
			case line, ok := <-events:
				if !ok {
					t.Fatalf("the event stream ended before %q", want)
				}
				if strings.Contains(line, want) {
					return
				}
			case <-deadline:
				t.Fatalf("no agent-state event with %q\n%s", want, term.Snapshot())
			}
		}
	}

	// report types a state report into the pane's shell, as an agent's hook runs
	// it, and waits for the event it raises. The pane is not focused: a client that
	// has the pane focused closes a finished item as soon as it sees it, which would
	// hide an item this test has to be able to see.
	typeReport := func(state, message string, extra ...string) {
		t.Helper()
		cmd := fmt.Sprintf("%q set-agent-state %s --harness pi -m %q %s", tuiosBin, state, message, strings.Join(extra, " "))
		if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-quiet", "-w", "WORKER", cmd+"\n"); err != nil {
			t.Fatalf("type the report: %v\n%s", err, out)
		}
	}
	report := func(state, message string, extra ...string) {
		t.Helper()
		typeReport(state, message, extra...)
		waitEvent(fmt.Sprintf(`"state":%q`, state))
	}
	inbox := func() string {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-attention", "--json")
		if err != nil {
			t.Fatalf("list-attention: %v\n%s", err, out)
		}
		return out
	}
	// waitInbox waits for a change of kind within one state, which raises no event.
	waitInbox := func(marker string, want bool) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			got := inbox()
			if strings.Contains(got, marker) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the Inbox holding %s is not %v:\n%s", marker, want, got)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	agent := func() map[string]any {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-agents", "-s", "e2e-quiet", "--json")
		if err != nil {
			t.Fatalf("list-agents: %v\n%s", err, out)
		}
		var res struct {
			Agents []map[string]any `json:"agents"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Agents) != 1 {
			t.Fatalf("list-agents: %v\n%s", err, out)
		}
		return res.Agents[0]
	}

	// The pane holds only read, so it cannot hide itself.
	runInShell(t, term, fmt.Sprintf("%q set-agent-quiet; echo RC''=$?", tuiosBin), "RC=", shellTimeout)
	if text := term.Screen().Text(); !strings.Contains(text, "RC=1") || !strings.Contains(text, "admin grant") {
		t.Fatalf("the pane marked itself quiet:\n%s", term.Snapshot())
	}
	runInShell(t, term, "clear; echo CLEA''RED", "CLEARED", shellTimeout)
	windowManagementMode(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "second window")
	renameWindow(t, term, "OTHER")

	// alerted reports whether a desktop notification named marker: the one sink of
	// an alert that a later one cannot cover, as a dock message can be queued
	// behind the one on show.
	alerted := func(marker string) bool {
		return regexp.MustCompile("\x1b\\]9;[^\x07]*" + marker).MatchString(host.String())
	}
	waitAlert := func(marker string) {
		t.Helper()
		if err := term.WaitFor(func(tuitest.Screen) bool { return alerted(marker) }, uiTimeout); err != nil {
			t.Fatalf("no alert named %s: %v\n%s", marker, err, term.Snapshot())
		}
	}

	// A loud turn opens its item and raises its alert.
	report("working", "starting")
	report("done", "LOUD-TURN")
	if got := inbox(); !strings.Contains(got, "LOUD-TURN") {
		t.Fatalf("a loud turn opened no item:\n%s", got)
	}
	waitAlert("LOUD-TURN")
	if agent()["quiet"] != false {
		t.Fatalf("a refused call changed the mark: %v", agent())
	}

	// The person marks it, and the open item closes.
	out, err := tuiosCLI(t, base, "set-agent-quiet", "-s", "e2e-quiet", "-w", "WORKER")
	if err != nil || !strings.Contains(out, "Inbox items closed: 1.") {
		t.Fatalf("set-agent-quiet: %v\n%s", err, out)
	}
	if got := inbox(); strings.Contains(got, "LOUD-TURN") {
		t.Fatalf("going quiet left the item open:\n%s", got)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		for row := range rows {
			if line := s.Line(row); strings.Contains(line, "WORKER") && strings.Contains(line, "quiet") {
				return true
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("the rail row does not say quiet: %v\n%s", err, term.Snapshot())
	}

	// A quiet turn, a question and a failure. The events come, list-agents reads
	// as for any pane, and neither the Inbox nor the dock hears of them.
	seqBefore, _ := agent()["completion_seq"].(float64)
	report("working", "starting again")
	report("done", "QUIET-TURN")
	report("needs_input", "QUIET-ASK", "--kind", "question")
	report("errored", "QUIET-FAIL")
	a := agent()
	if a["quiet"] != true || a["state"] != "errored" {
		t.Fatalf("list-agents does not read as for any pane: %v", a)
	}
	if seqNow, _ := a["completion_seq"].(float64); seqNow != seqBefore+1 {
		t.Fatalf("completion_seq went from %v to %v, want one more", seqBefore, seqNow)
	}
	if got := inbox(); strings.Contains(got, "QUIET-") || strings.Contains(got, `"kind"`) {
		t.Fatalf("a quiet pane reached the Inbox:\n%s", got)
	}
	// A question that becomes an approval with no change of state is the first the
	// person hears of it, in the Inbox and in the alerts.
	report("needs_input", "QUIET-ASK2", "--kind", "question")
	typeReport("needs_input", "QUIET-APPROVE", "--kind", "approval")
	waitInbox("QUIET-APPROVE", true)
	// Alerts go out in order, so with this one here the ones before it would have
	// been too.
	waitAlert("QUIET-APPROVE")
	for _, marker := range []string{"QUIET-TURN", "QUIET-ASK", "QUIET-FAIL"} {
		if alerted(marker) {
			t.Fatalf("a quiet pane raised an alert named %s", marker)
		}
	}
	// An approval that becomes a question leaves no stale approval behind.
	typeReport("needs_input", "QUIET-ASK3", "--kind", "question")
	waitInbox("QUIET-APPROVE", false)
	if got := inbox(); strings.Contains(got, "QUIET-ASK3") {
		t.Fatalf("a quiet pane's question reached the Inbox:\n%s", got)
	}
	// A login gets through too, and going quiet leaves it open.
	typeReport("needs_input", "QUIET-LOGIN", "--kind", "auth")
	waitInbox("QUIET-LOGIN", true)
	waitAlert("QUIET-LOGIN")
	if out, err := tuiosCLI(t, base, "set-agent-quiet", "-s", "e2e-quiet", "-w", "WORKER"); err != nil || !strings.Contains(out, "Inbox items closed: 0.") {
		t.Fatalf("set-agent-quiet with a login open: %v\n%s", err, out)
	}
	waitInbox("QUIET-LOGIN", true)
	report("working", "logged in")

	// Going loud while the pane waits raises the question it waits on, with its
	// alert. The turns and the failure from before stay closed.
	report("needs_input", "QUIET-OPEN", "--kind", "question")
	if got := inbox(); strings.Contains(got, "QUIET-OPEN") {
		t.Fatalf("a quiet pane's question reached the Inbox:\n%s", got)
	}
	if out, err := tuiosCLI(t, base, "set-agent-quiet", "-s", "e2e-quiet", "-w", "WORKER", "--off"); err != nil || !strings.Contains(out, "is not quiet") {
		t.Fatalf("set-agent-quiet --off: %v\n%s", err, out)
	}
	waitInbox("QUIET-OPEN", true)
	waitAlert("QUIET-OPEN")
	if got := inbox(); strings.Contains(got, "QUIET-TURN") || strings.Contains(got, "QUIET-FAIL") || strings.Contains(got, "LOUD-TURN") {
		t.Fatalf("going loud opened an old item:\n%s", got)
	}
	report("working", "answered")
	waitInbox("QUIET-OPEN", false)

	report("done", "LOUD-AGAIN")
	if got := inbox(); !strings.Contains(got, "LOUD-AGAIN") {
		t.Fatalf("a turn after going loud opened no item:\n%s", got)
	}
	if agent()["quiet"] != false {
		t.Fatalf("list-agents still says quiet: %v", agent())
	}

	// A pane with admin may mark a worker, as a moderator does, but it does not
	// close a question for the person. The person can.
	report("needs_input", "LOUD-ASK", "--kind", "question")
	waitInbox("LOUD-ASK", true)
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "e2e-quiet", "-w", "OTHER", "--grants", "admin"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	enterTerminalMode(t, term)
	runInShell(t, term, fmt.Sprintf("%q set-agent-quiet -s e2e-quiet -w WORKER; echo RC''=$?", tuiosBin), "RC=", shellTimeout)
	if text := term.Screen().Text(); !strings.Contains(text, "RC=0") {
		t.Fatalf("a pane with admin could not mark a worker:\n%s", term.Snapshot())
	}
	if agent()["quiet"] != true {
		t.Fatalf("the mark did not land: %v", agent())
	}
	waitInbox("LOUD-ASK", true)
	if out, err := tuiosCLI(t, base, "set-agent-quiet", "-s", "e2e-quiet", "-w", "WORKER"); err != nil || !strings.Contains(out, "Inbox items closed: 1.") {
		t.Fatalf("the person marking the worker: %v\n%s", err, out)
	}
	waitInbox("LOUD-ASK", false)

	// The mark goes with the agent, so the next one in the pane does not inherit it.
	report("none", "left")
	report("working", "the next agent")
	if agent()["quiet"] != false {
		t.Fatalf("the next agent inherited the mark: %v", agent())
	}

	// A pane with no agent has nothing to mark, and no row would show it.
	if out, err := tuiosCLI(t, base, "set-agent-quiet", "-s", "e2e-quiet", "-w", "OTHER"); err == nil || !strings.Contains(out, "runs no agent") {
		t.Fatalf("set-agent-quiet on a pane with no agent: %v\n%s", err, out)
	}
	alive(t, term, "after the quiet turns")
}
