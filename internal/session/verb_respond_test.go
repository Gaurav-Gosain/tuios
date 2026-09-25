package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAgentScript is a stand-in for an agent blocked on a prompt, run in a
// real pane. Its first argument picks what it does, and every key or line it
// reads is appended to the file named by its second, so a test can say
// exactly what reached the agent.
//
//	approve   Claude Code's permission menu; one key, then the working footer
//	stubborn  the same menu; every key is read and the menu stays as it was
//	text      a question answered in words, read as a line
const fakeAgentScript = `#!/bin/sh
out=$2
menu() {
  printf '\033[2J\033[H Bash command\r\n   rm -rf build\r\n Do you want to proceed?\r\n \342\235\257 1. Yes\r\n   2. Yes, and don'\''t ask again for rm commands\r\n   3. No, and tell Claude what to do differently (esc)\r\n'
}
case $1 in
approve)
  stty raw -echo
  menu
  k=$(dd bs=1 count=1 2>/dev/null)
  printf '%s' "$k" >> "$out"
  printf '\033[2J\033[Hanswered\r\n\342\217\265\342\217\265 auto mode on \302\267 esc to interrupt\r\n'
  sleep 60;;
stubborn)
  stty raw -echo
  menu
  while k=$(dd bs=1 count=1 2>/dev/null); do printf '%s' "$k" >> "$out"; done;;
text)
  stty -echo
  printf '\033[2J\033[HWhich branch?\r\n'
  read -r line
  printf '%s' "$line" >> "$out"
  printf '\033[2J\033[Hworking on it\r\n'
  sleep 60;;
esac
`

// blockedAgent is a pane running the fake agent on a prompt, attributed to a
// harness, and the file its input lands in.
type blockedAgent struct {
	d      *Daemon
	sp     string
	sess   *Session
	window string
	other  string
	got    string
}

// startBlockedAgent runs the fake agent in mode, in a pane attributed to
// harnessID, and waits for the screen tier to put the pane on needs_input.
func startBlockedAgent(t *testing.T, d *Daemon, sp, sessName, harnessID, mode string) *blockedAgent {
	t.Helper()
	sess, a, b := twoWindowSession(t, d, sessName)
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(script, []byte(fakeAgentScript), 0o700); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(dir, "got")
	// The pane is attributed the way the detector would, so the screen tier
	// runs the harness's rules on it.
	if _, _, err := sess.ApplyAgentReport(a, AgentReport{State: AgentStateWorking, Source: AgentSourceScreen, Harness: harnessID}); err != nil {
		t.Fatal(err)
	}
	runInPane(t, d, sess, a, "sh "+script+" "+mode+" "+got)
	deadline := time.Now().Add(10 * time.Second * testDeadlineScale)
	for agentStateOf(t, sess, a) != AgentStateNeedsInput {
		if time.Now().After(deadline) {
			t.Fatalf("the pane never went to needs_input: %s", agentStateOf(t, sess, a))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &blockedAgent{d: d, sp: sp, sess: sess, window: a, other: b, got: got}
}

// received is what the agent has read so far.
func (b *blockedAgent) received() string {
	data, _ := os.ReadFile(b.got)
	return string(data)
}

// call makes one verb call on c and returns the raw response.
func callVerb(t *testing.T, c *verbConn, verb string, params map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return c.call(t, fmt.Sprintf(`{"id":1,"verb":%q,"params":%s}`, verb, raw))
}

func peek(t *testing.T, c *verbConn, sess, window string) map[string]any {
	t.Helper()
	return result(t, callVerb(t, c, "peek-prompt", map[string]any{"session": sess, "window": window}))
}

// TestPeekAndRespondAnswerAClaudeApproval is the feature end to end in the
// daemon: a real pane running an agent that painted Claude Code's permission
// menu and waits for a key. Peek reads the menu and what may answer it;
// respond refuses a caller with no attach, refuses a prompt_id that does not
// name the prompt on the screen, and then presses 1 for approve and returns
// the state the pane moved to.
//
// Negative control: with the prompt_id check removed from verbRespond, the
// stale-id call presses a key and the assertion on what the agent read fails.
func TestPeekAndRespondAnswerAClaudeApproval(t *testing.T) {
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "resp", "claude-code", "approve")
	c := dialVerb(t, sp)

	pk := peek(t, c, "resp", ag.window)
	if pk["found"] != true || pk["answerable"] != true || pk["kind"] != "approval" || pk["harness"] != "claude-code" {
		t.Fatalf("peek: %v", pk)
	}
	if acts := fmt.Sprint(pk["actions"]); acts != "[approve approve_always deny choose]" {
		t.Errorf("actions %s", acts)
	}
	if opts, _ := pk["options"].([]any); len(opts) != 3 {
		t.Errorf("options %v", pk["options"])
	}
	if lines := fmt.Sprint(pk["lines"]); !strings.Contains(lines, "rm -rf build") || !strings.Contains(lines, "Do you want to proceed?") {
		t.Errorf("lines %s", lines)
	}
	id, _ := pk["prompt_id"].(string)
	if id == "" || pk["untrusted"] != true {
		t.Fatalf("peek has no prompt_id or is not marked untrusted: %v", pk)
	}

	params := map[string]any{"session": "resp", "window": ag.window, "action": "approve", "prompt_id": id}
	if code := errCode(t, callVerb(t, c, "respond", params)); code != ErrVerbNotHuman {
		t.Fatalf("respond with no nonce: %s, want %s", code, ErrVerbNotHuman)
	}

	tui := attachTUI(t, sp, "resp")
	params["human_nonce"] = tui.HumanNonce()
	stale := map[string]any{}
	for k, v := range params {
		stale[k] = v
	}
	stale["prompt_id"] = "0000000000000000"
	if code := errCode(t, callVerb(t, c, "respond", stale)); code != ErrVerbPromptChanged {
		t.Fatalf("respond with a stale prompt_id: %s, want %s", code, ErrVerbPromptChanged)
	}
	bad := map[string]any{}
	for k, v := range params {
		bad[k] = v
	}
	bad["action"], bad["value"] = "choose", "7"
	if code := errCode(t, callVerb(t, c, "respond", bad)); code != ErrVerbInvalidParams {
		t.Fatalf("respond choosing an option that is not there: %s, want %s", code, ErrVerbInvalidParams)
	}
	if got := ag.received(); got != "" {
		t.Fatalf("ASSERTION: a refused respond reached the agent: %q", got)
	}

	res := result(t, callVerb(t, c, "respond", params))
	if res["sent"] != "1" || res["prompt_id"] != id {
		t.Errorf("respond: %v", res)
	}
	if res["settled_by"] != respondSettledState || res["state"] != "working" {
		t.Errorf("respond did not wait for the pane to move on: %v", res)
	}
	if got := ag.received(); got != "1" {
		t.Errorf("the agent read %q, want 1", got)
	}

	// Answered, the pane is no longer blocked, and a peek says so.
	after := peek(t, c, "resp", ag.window)
	if after["blocked"] != false || after["found"] != false || after["answerable"] != false {
		t.Errorf("peek after the answer: %v", after)
	}
	if code := errCode(t, callVerb(t, c, "respond", params)); code != ErrVerbPromptChanged {
		t.Errorf("a second answer to a prompt that is gone: %s, want %s", code, ErrVerbPromptChanged)
	}
}

// TestRespondFirstAnswerWins: two clients answer the same prompt at once. One
// answer reaches the agent and the other is refused with prompt_changed.
//
// Negative control: with the per-window slot lock removed from verbRespond,
// both calls pass the check before either presses, and the agent reads two
// keys.
func TestRespondFirstAnswerWins(t *testing.T) {
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "race", "claude-code", "approve")
	c := dialVerb(t, sp)
	id := peek(t, c, "race", ag.window)["prompt_id"].(string)
	tui := attachTUI(t, sp, "race")

	var wg sync.WaitGroup
	codes := make([]string, 2)
	for i, action := range []string{"approve", "deny"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := dialVerb(t, sp)
			resp := callVerb(t, conn, "respond", map[string]any{
				"session": "race", "window": ag.window, "action": action,
				"prompt_id": id, "human_nonce": tui.HumanNonce(),
			})
			if e, ok := resp["error"].(map[string]any); ok {
				codes[i], _ = e["code"].(string)
			} else {
				codes[i] = "ok"
			}
		}()
	}
	wg.Wait()
	if !(codes[0] == "ok" && codes[1] == ErrVerbPromptChanged) && !(codes[1] == "ok" && codes[0] == ErrVerbPromptChanged) {
		t.Fatalf("two answers to one prompt gave %v, want one ok and one %s", codes, ErrVerbPromptChanged)
	}
	if got := ag.received(); len(got) != 1 {
		t.Fatalf("ASSERTION: the agent read %q, want exactly one key", got)
	}
}

// TestRespondDoesNotAnswerTheSamePromptTwice: an answer that did not move the
// prompt (the agent drew the same menu again) is not answered a second time.
// Without a prompt_id the caller asked for whatever is on the screen, and what
// is on the screen is the prompt that was already answered.
func TestRespondDoesNotAnswerTheSamePromptTwice(t *testing.T) {
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "twice", "claude-code", "stubborn")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "twice")
	params := map[string]any{"session": "twice", "window": ag.window, "action": "approve", "human_nonce": tui.HumanNonce(), "timeout": 400}

	res := result(t, callVerb(t, c, "respond", params))
	if res["settled_by"] != respondSettledTimeout || res["state"] != "needs_input" {
		t.Errorf("an answer the agent ignored: %v", res)
	}
	if code := errCode(t, callVerb(t, c, "respond", params)); code != ErrVerbPromptChanged {
		t.Fatalf("answering the answered prompt again: %s, want %s", code, ErrVerbPromptChanged)
	}
	if got := ag.received(); got != "1" {
		t.Errorf("the agent read %q, want one 1", got)
	}
}

// TestRespondSlotsStayBounded: the map of answered windows does not grow
// without end, and a slot a call has taken is never dropped from under it,
// even before the call locks it.
//
// Negative control: evicting by TryLock, as a first draft did, drops the slot
// taken but not yet locked, and the last check fails.
func TestRespondSlotsStayBounded(t *testing.T) {
	var r respondSlots
	held := r.slot("held")
	for i := range maxRespondSlots * 2 {
		r.release(r.slot(fmt.Sprint(i)))
	}
	if n := len(r.slots); n > maxRespondSlots {
		t.Errorf("%d slots, bound %d", n, maxRespondSlots)
	}
	if r.slot("held") != held {
		t.Error("a taken slot was dropped")
	}
}

// runInPane types a command into a pane's shell.
func runInPane(t *testing.T, d *Daemon, sess *Session, window, line string) {
	t.Helper()
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	waitForQuiet(t, pty, 200*time.Millisecond, 5*time.Second)
	if _, err := pty.Write([]byte(line + "\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
}
