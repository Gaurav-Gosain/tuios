package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A pane reports agent state only for its own window, on a real daemon in the
// default open mode, where every pane holds admin.
//
// How these could pass wrongly, written down first:
//   - The attacker's send-keys could fail for a reason that is not the
//     prompt rule, and the victim would stay unanswered either way. So the
//     respond half sends the same keys from the same pane and watches them
//     answer the prompt.
//   - The victim's own report could fail too, and needs_input would never be
//     set. The test waits for needs_input before the attacker runs, so the
//     report from inside the victim's own pane is proved to work.
//   - A refusal could come from the CLI alone. The pane prints the daemon's
//     message, which names the respond grant.
//   - "No ANSWERED line" could be read before the keys land. The pane is
//     read again a second after the attacker's line finishes.

// victimPrompt reports needs_input for its own pane with no --window, the way
// a harness hook does, then asks and waits for an answer. It draws the
// question again on SIGWINCH, as Ink and ratatui prompts do, and says REDRAWN
// when it does.
const victimPrompt = `#!/usr/bin/env bash
"$TUIOS_E2E_CLI" set-agent-state needs_input --kind approval --message 'approve Bash: rm -rf build'
ask() { printf 'Allow the edit? [y/n] '; }
trap 'printf "\nREDRAWN "; ask' WINCH
ask
# A short timeout lets the trap run while it waits.
until read -r -t 0.2 ans; do :; done
printf 'ANSWERED:%s\n' "$ans"
`

// TestAPaneCannotMarkAnotherPaneToAnswerItsPrompt runs the reported steps.
// Pane B (focused) marks pane A working with set-agent-state -w A, then sends
// "y Enter" to A, which waits on a prompt. Without respond both calls are
// refused and A's prompt is not answered. The person's CLI, outside every
// pane, may still mark A; B still may not type into the prompt that is on
// A's screen. With respond, B marks A and answers its prompt.
//
// Between the person's mark and B's keys, B resizes A, and A draws its
// prompt again. That output must not end the hold.
//
// With respond, B's keys into the prompt are still refused until B passes
// --allow-blocked.
//
// Negative control: on origin/main SET_EXIT=0 and SK_EXIT=0 appear and A
// shows ANSWERED:y. With promptStillShown cut from typingRefusal, or with the
// hold ended by any output after it, the person half fails: B's keys answer
// the prompt after the person marked A working and B resized A.
func TestAPaneCannotMarkAnotherPaneToAnswerItsPrompt(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	script := filepath.Join(base, "victim.sh")
	if err := os.WriteFile(script, []byte(victimPrompt), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "victim", "--no-focus"); err != nil {
		t.Fatalf("new-window failed: %v\n%s", err, out)
	}
	ids := grantWindowIDs(t, base, sess)
	if len(ids) != 2 {
		t.Fatalf("want two windows, got %v", ids)
	}
	attacker, victim := ids[0], ids[1]

	state := func() string {
		out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", sess, "-w", victim)
		if err != nil {
			t.Fatalf("get-agent-state: %v\n%s", err, out)
		}
		var r struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("get-agent-state json: %v\n%s", err, out)
		}
		return r.State
	}
	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for got := state(); got != want; got = state() {
			if time.Now().After(deadline) {
				t.Fatalf("victim state is %q, want %q", got, want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	victimText := func() string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", victim)
		return out
	}
	// fromAttacker types line into B and waits for marker=<code> on B's screen.
	fromAttacker := func(line, marker string) string {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", attacker, "clear; "+line+"\n"); err != nil {
			t.Fatalf("send-text into B failed: %v\n%s", err, out)
		}
		var text string
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text = strings.Join(strings.Fields(s.Text()), " ")
			return strings.Contains(text, marker+"=0") || strings.Contains(text, marker+"=1")
		}, uiTimeout); err != nil {
			t.Fatalf("B's line never finished: %v\n%s", err, term.Snapshot())
		}
		return text
	}

	// The victim reports for itself, with no --window, and waits on a prompt.
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", victim, "TUIOS_E2E_CLI="+tuiosBin+" bash "+script+"\n"); err != nil {
		t.Fatalf("start the victim: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(victimText(), "Allow the edit? [y/n]") {
		if time.Now().After(deadline) {
			t.Fatalf("the victim never asked:\n%s", victimText())
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitState("needs_input")

	// The reported steps, from pane B.
	text := fromAttacker(tuiosBin+" set-agent-state -s "+sess+" -w "+victim+" working; echo SET_\"EXIT\"=$?; "+
		tuiosBin+" send-keys -s "+sess+" -w "+victim+" 'y Enter'; echo SK_\"EXIT\"=$?", "SK_EXIT")
	time.Sleep(time.Second)
	saveFrame(t, term, "agent-state-other-pane-refused")
	if strings.Contains(victimText(), "ANSWERED") {
		t.Fatalf("ASSERTION: B answered A's prompt without respond:\n%s", victimText())
	}
	if !strings.Contains(text, "SET_EXIT=1") {
		t.Errorf("ASSERTION: B set A's agent state without respond:\n%s", text)
	}
	if !strings.Contains(text, "SK_EXIT=1") || !strings.Contains(text, "respond") {
		t.Errorf("ASSERTION: B's keys were not refused for the respond grant:\n%s", text)
	}
	if got := state(); got != "needs_input" {
		t.Errorf("ASSERTION: A's state moved to %q after B was refused", got)
	}

	// The person, outside every pane, may mark A. The prompt is still on A's
	// screen, so B still may not type into it.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", sess, "-w", victim, "working"); err != nil {
		t.Fatalf("ASSERTION: the person's set-agent-state was refused: %v\n%s", err, out)
	}
	waitState("working")

	// B makes A draw its prompt again by resizing it. Output after the
	// prompt does not show the prompt is gone.
	if text := fromAttacker(tuiosBin+" call resize '{\"session\":\""+sess+"\",\"window\":\""+victim+"\",\"width\":50,\"height\":16}'; echo RS_\"EXIT\"=$?", "RS_EXIT"); !strings.Contains(text, "RS_EXIT=0") {
		t.Fatalf("B's resize of A failed:\n%s", text)
	}
	deadline = time.Now().Add(uiTimeout)
	for !strings.Contains(victimText(), "REDRAWN") {
		if time.Now().After(deadline) {
			t.Fatalf("A never drew its prompt again after the resize:\n%s", victimText())
		}
		time.Sleep(100 * time.Millisecond)
	}
	text = fromAttacker(tuiosBin+" send-keys -s "+sess+" -w "+victim+" 'y Enter'; echo SK2_\"EXIT\"=$?", "SK2_EXIT")
	time.Sleep(time.Second)
	saveFrame(t, term, "agent-state-prompt-still-shown")
	if strings.Contains(victimText(), "ANSWERED") {
		t.Fatalf("ASSERTION: B answered A's prompt after the person marked A working:\n%s", victimText())
	}
	if !strings.Contains(text, "SK2_EXIT=1") || !strings.Contains(text, "respond") {
		t.Errorf("ASSERTION: B's keys into the prompt still shown were not refused:\n%s", text)
	}

	// With respond, B may mark A. Its keys into A's prompt still need
	// --allow-blocked, which says B read the prompt and means to answer it.
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", sess, "-w", attacker, "--grants", "admin,respond"); err != nil {
		t.Fatalf("give B respond: %v\n%s", err, out)
	}
	text = fromAttacker(tuiosBin+" set-agent-state -s "+sess+" -w "+victim+" needs_input; echo SET3_\"EXIT\"=$?; "+
		tuiosBin+" send-keys -s "+sess+" -w "+victim+" 'y Enter'; echo SK3_\"EXIT\"=$?", "SK3_EXIT")
	if !strings.Contains(text, "SET3_EXIT=0") {
		t.Errorf("ASSERTION: B with respond could not mark A:\n%s", text)
	}
	if !strings.Contains(text, "SK3_EXIT=1") || !strings.Contains(text, "allow-blocked") {
		t.Errorf("ASSERTION: B's keys into A's prompt without --allow-blocked were not refused:\n%s", text)
	}
	time.Sleep(time.Second)
	if strings.Contains(victimText(), "ANSWERED") {
		t.Fatalf("ASSERTION: B answered A's prompt without --allow-blocked:\n%s", victimText())
	}
	text = fromAttacker(tuiosBin+" send-keys -s "+sess+" -w "+victim+" --allow-blocked 'y Enter'; echo SK4_\"EXIT\"=$?", "SK4_EXIT")
	if !strings.Contains(text, "SK4_EXIT=0") {
		t.Errorf("ASSERTION: B with respond and --allow-blocked was refused:\n%s", text)
	}
	deadline = time.Now().Add(uiTimeout)
	for !strings.Contains(victimText(), "ANSWERED:y") {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: B with respond did not answer A's prompt:\n%s", victimText())
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveFrame(t, term, "agent-state-respond-answers")
	alive(t, term, "after the agent state checks")
}

// TestADaemonChildCannotMarkAnotherPane: a process the daemon starts outside
// every pane, here an after-new-window hook, runs set-agent-state -w A. A
// pane can get code run that way (git run by the daemon in a folder the pane
// controls, before core.fsmonitor was turned off for it), so such a process
// may not report for a pane. A waits on a prompt and stays on needs_input.
//
// How it could pass wrongly: the hook might never run, and A would keep its
// state anyway. The hook writes its exit code, and the test waits for it.
//
// Negative control: on origin/main, and with the unplaced exemption back in
// holdOwnRecord, HOOK_EXIT=0 appears and A moves to working.
func TestADaemonChildCannotMarkAnotherPane(t *testing.T) {
	const sess = "e2e-child"
	base := t.TempDir()
	killDaemon(t, base)
	armed := filepath.Join(base, "armed")
	out := filepath.Join(base, "hook.out")
	hook := filepath.Join(base, "hook.sh")
	body := "#!/bin/sh\n[ -e " + armed + " ] || exit 0\nrm -f " + armed + "\n" +
		tuiosBin + " set-agent-state -s " + sess + " -w victim working > " + out + ".tmp 2>&1\n" +
		"echo HOOK_EXIT=$? >> " + out + ".tmp\nmv " + out + ".tmp " + out + "\n"
	if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[hooks]\nafter-new-window = \""+hook+"\"\n")
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "victim", "--no-focus"); err != nil {
		t.Fatalf("open the victim: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", sess, "-w", "victim", "needs_input", "--kind", "approval"); err != nil {
		t.Fatalf("the person's set-agent-state failed: %v\n%s", err, out)
	}
	if err := os.WriteFile(armed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "trigger", "--no-focus"); err != nil {
		t.Fatalf("open the trigger window: %v\n%s", err, out)
	}
	var got []byte
	deadline := time.Now().Add(uiTimeout)
	for {
		b, err := os.ReadFile(out)
		if err == nil && strings.Contains(string(b), "HOOK_EXIT=") {
			got = b
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hook never ran set-agent-state")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "daemon-child-set-agent-state.txt"), got, 0o644)
	}
	flat := strings.Join(strings.Fields(string(got)), " ")
	if !strings.Contains(flat, "HOOK_EXIT=1") || !strings.Contains(flat, "respond") {
		t.Errorf("ASSERTION: the daemon's child was not refused for another pane:\n%s", got)
	}
	state, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", sess, "-w", "victim")
	if err != nil {
		t.Fatalf("get-agent-state: %v\n%s", err, state)
	}
	var r struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(state), &r); err != nil {
		t.Fatalf("get-agent-state json: %v\n%s", err, state)
	}
	if r.State != "needs_input" {
		t.Errorf("ASSERTION: the daemon's child moved the victim to %q", r.State)
	}
}
