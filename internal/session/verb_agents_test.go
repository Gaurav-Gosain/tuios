package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// twoWindowSession returns a session with two live daemon windows and their ids,
// which is the smallest shape the cross-agent verbs need: a sender and a
// recipient.
func twoWindowSession(t *testing.T, d *Daemon, name string) (*Session, string, string) {
	t.Helper()
	sess := makeSessionWithWindow(t, d, name)
	if _, err := sess.AddDaemonWindow("Second", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	st := sess.GetState()
	if len(st.Windows) < 2 {
		t.Fatalf("wanted two windows, got %d", len(st.Windows))
	}
	return sess, st.Windows[0].ID, st.Windows[1].ID
}

// TestReadingWithoutAnInboxMarksNothing pins the rule that keeps one agent from
// emptying another's mailbox as a side effect of looking around.
func TestReadingWithoutAnInboxMarksNothing(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "peek")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"peek","to":"`+b+`","from":"`+a+`","text":"for b only"}}`)
	result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"peek"}}`))

	unread := result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"peek","to":"`+b+`","unread":true}}`))
	if n, _ := unread["messages"].([]any); len(n) != 1 {
		t.Errorf("a session-wide read consumed the recipient's mail: %v", unread)
	}
}

// TestPeekDoesNotMarkRead covers the explicit opt-out.
func TestPeekDoesNotMarkRead(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "nopeek")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"nopeek","to":"`+b+`","from":"`+a+`","text":"still unread"}}`)
	result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"nopeek","to":"`+b+`","peek":true}}`))

	unread := result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"nopeek","to":"`+b+`","unread":true}}`))
	if n, _ := unread["messages"].([]any); len(n) != 1 {
		t.Errorf("peek marked the message read: %v", unread)
	}
}

// TestNoticeHasNoRecipient covers the second addressing mode: a message with no
// recipient is readable by everyone and unread by nobody.
func TestNoticeHasNoRecipient(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "notice")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"notice","from":"`+a+`","text":"deploying soon"}}`))
	if res["kind"] != agentMsgNotice {
		t.Errorf("kind = %v, want %s", res["kind"], agentMsgNotice)
	}

	// It never counts as unread mail for anyone.
	unread := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"notice","to":"`+b+`","unread":true}}`))
	if n, _ := unread["messages"].([]any); len(n) != 0 {
		t.Errorf("a notice showed up as unread mail: %v", unread)
	}
	// And an inbox read that asks for notices sees it.
	withNotices := result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"notice","to":"`+b+`","notices":true}}`))
	if n, _ := withNotices["messages"].([]any); len(n) != 1 {
		t.Errorf("an inbox read asking for notices did not see one: %v", withNotices)
	}
}

// TestSelfAddressedMessageIsRefused is the shortest loop there is.
func TestSelfAddressedMessageIsRefused(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "self")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"self","to":"`+a+`","from":"`+a+`","text":"note to self"}}`)
	if code := errCode(t, resp); code != ErrVerbLoopRefused {
		t.Errorf("code = %q, want %q", code, ErrVerbLoopRefused)
	}
}

// TestMessageToAClosedWindowReadsUndeliverable pins the honest answer about a
// window's inbox dying with the window: the message stays in the log and says
// it was never delivered, rather than being re-homed onto whatever pane takes
// the old one's name.
func TestMessageToAClosedWindowReadsUndeliverable(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "gone")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"gone","to":"`+b+`","from":"`+a+`","text":"are you there"}}`)
	if _, err := sess.CloseDaemonWindow(b); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}

	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"gone"}}`))
	msgs, _ := read["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if m := msgs[0].(map[string]any); m["undeliverable"] != true {
		t.Errorf("a message to a closed window did not read undeliverable: %v", m)
	}
}

// TestRateCapRefusesAFlood covers the bound that stops two agents answering each
// other from running forever.
func TestRateCapRefusesAFlood(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "flood")
	c := dialVerb(t, sp)

	call := `{"id":1,"verb":"send-agent-message","params":{"session":"flood","to":"` + b + `","from":"` + a + `","text":"x"}}`
	for i := range agentSendBurst {
		if resp := c.call(t, call); resp["error"] != nil {
			t.Fatalf("send %d was refused inside the burst: %v", i, resp["error"])
		}
	}
	if code := errCode(t, c.call(t, call)); code != ErrVerbRateLimited {
		t.Errorf("code = %q, want %q", code, ErrVerbRateLimited)
	}
}

// TestRingEvictsAndSaysSo pins the other bound: a full ring drops its oldest and
// reports the count, rather than growing or losing messages quietly.
func TestRingEvictsAndSaysSo(t *testing.T) {
	bus := newAgentBus()
	for i := 0; i < agentMailboxMaxMessages+5; i++ {
		bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "x"})
	}
	res := bus.read("s", readQuery{limit: 1000})
	if res.Total != agentMailboxMaxMessages {
		t.Errorf("ring holds %d, want the cap of %d", res.Total, agentMailboxMaxMessages)
	}
	if res.Evicted != 5 {
		t.Errorf("evicted = %d, want 5", res.Evicted)
	}
}

// TestRingIsBoundedByBytesToo covers the second cap, which is the one that
// matters when every message is at the size limit.
func TestRingIsBoundedByBytesToo(t *testing.T) {
	bus := newAgentBus()
	big := strings.Repeat("x", agentMsgMaxText)
	for range 200 {
		bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: big})
	}
	bus.mu.Lock()
	held := bus.box("s").bytes
	bus.mu.Unlock()
	if held > agentMailboxMaxBytes {
		t.Errorf("ring holds %d bytes, over the %d cap", held, agentMailboxMaxBytes)
	}
}

func TestOversizedMessageIsRefused(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "big")
	c := dialVerb(t, sp)

	params, err := json.Marshal(map[string]any{
		"session": "big", "to": b, "from": a,
		"text": strings.Repeat("x", agentMsgMaxText+1),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := c.call(t, `{"id":1,"verb":"send-agent-message","params":`+string(params)+`}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestAttachmentIsAReferenceNotBytes covers the payload decision: the ring holds
// a path and the reader is told whether the file is still there.
func TestAttachmentIsAReferenceNotBytes(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "attach")
	c := dialVerb(t, sp)

	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("not really a png"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"attach","to":"`+b+`","from":"`+a+`","text":"look","attachments":["`+img+`"]}}`)
	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"attach","to":"`+b+`","peek":true}}`))
	m := read["messages"].([]any)[0].(map[string]any)
	atts, _ := m["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachment did not survive: %v", m)
	}
	att := atts[0].(map[string]any)
	if att["kind"] != "image" || att["media_type"] != "image/png" || att["path"] != img {
		t.Errorf("attachment classified wrong: %v", att)
	}
	if att["missing"] == true {
		t.Error("an attachment whose file exists reported missing")
	}

	// The producer owns the file, so removing it is visible to the reader rather
	// than hidden by a copy the queue never made.
	if err := os.Remove(img); err != nil {
		t.Fatalf("remove: %v", err)
	}
	read = result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"attach","to":"`+b+`","peek":true}}`))
	m = read["messages"].([]any)[0].(map[string]any)
	att = m["attachments"].([]any)[0].(map[string]any)
	if att["missing"] != true {
		t.Errorf("a deleted attachment did not read missing: %v", att)
	}
}

func TestAttachmentMustBeAnExistingAbsolutePath(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "badattach")
	c := dialVerb(t, sp)

	for _, path := range []string{"relative/path.png", "/nonexistent/nope.png"} {
		resp := c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"badattach","to":"`+b+`","from":"`+a+`","text":"look","attachments":["`+path+`"]}}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("%s: code = %q, want %q", path, code, ErrVerbInvalidParams)
		}
	}
}

// TestWaitForAgentMessageMatchesMailAlreadyWaiting covers the race a poll loop
// would have had to work around.
func TestWaitForAgentMessageMatchesMailAlreadyWaiting(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "waitmail")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"waitmail","to":"`+b+`","from":"`+a+`","subject":"early","text":"sent before the wait"}}`)
	res := result(t, c.call(t, `{"id":2,"verb":"wait-for","params":{"condition":"agent-message","session":"waitmail","window":"`+b+`","timeout":2000}}`))
	if res["matched"] != true || res["subject"] != "early" {
		t.Errorf("wait did not match a message already in the inbox: %v", res)
	}
}

// TestAskCycleIsRefusedBeforeItSpins is the loop guard for the synchronous path.
// The graph is checked before the edge is added, so B asking A back while A is
// still blocked on B fails instead of deadlocking both.
func TestAskCycleIsRefusedBeforeItSpins(t *testing.T) {
	bus := newAgentBus()
	if !bus.openAsk("a", "b") {
		t.Fatal("the first edge was refused")
	}
	if bus.openAsk("b", "a") {
		t.Error("an edge closing a cycle was allowed")
	}
	// And a longer cycle: a -> b -> c -> a.
	if !bus.openAsk("b", "c") {
		t.Fatal("b -> c was refused")
	}
	if bus.openAsk("c", "a") {
		t.Error("a three-hop cycle was allowed")
	}
	// Releasing the edge opens the path again.
	bus.closeAsk("a", "b")
	if !bus.openAsk("b", "a") {
		t.Error("the edge stayed blocked after the ask it belonged to finished")
	}
}

func TestAskRefusesToAskItself(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "askself")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"askself","window":"`+a+`","from":"`+a+`","text":"hello"}}`)
	if code := errCode(t, resp); code != ErrVerbLoopRefused {
		t.Errorf("code = %q, want %q", code, ErrVerbLoopRefused)
	}
}

// TestAskWaitsForAWorkingAgent covers the readiness gate: an agent mid-turn is
// not typed at, and the failure names the remedy rather than the clock.
func TestAskWaitsForAWorkingAgent(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "busy")
	c := dialVerb(t, sp)

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateWorking, Message: "mid turn"}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}

	resp := c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"busy","window":"`+b+`","from":"`+a+`","text":"hello","ready_timeout":250}}`)
	if code := errCode(t, resp); code != ErrVerbNotReady {
		t.Fatalf("code = %q, want %q", code, ErrVerbNotReady)
	}
	e := resp["error"].(map[string]any)
	if !strings.Contains(e["message"].(string), "still working") {
		t.Errorf("refusal did not say why: %v", e["message"])
	}
}

// TestAskRefusesABlockedAgent covers the permission menu. An agent on
// needs_input is waiting on a prompt, and text typed there is read as the
// answer, so ask-agent refuses it with agent_blocked and writes nothing. force
// does not change that; only allow_blocked does.
func TestAskRefusesABlockedAgent(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "blocked")
	c := dialVerb(t, sp)

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{
		State: AgentStateNeedsInput, Message: "Do you want to run rm -rf build?", Kind: "approval",
	}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	pty, err := d.resolvePTYForTarget(sess, b)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	// A shell echoes what is typed at it, so a pane that was written to prints
	// the marker. Wait for the prompt to be drawn first, so the comparison is
	// between two settled screens.
	waitForQuiet(t, pty, 300*time.Millisecond, 5*time.Second)
	before := pty.CaptureContent(true, false)

	for _, params := range []string{
		`"text":"echo tuios_blocked_marker"`,
		`"text":"echo tuios_blocked_marker","force":true`,
	} {
		resp := c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"blocked","window":"`+b+`","from":"`+a+`",`+params+`,"ready_timeout":250}}`)
		if code := errCode(t, resp); code != ErrVerbAgentBlocked {
			t.Fatalf("with %s: code = %q, want %q", params, code, ErrVerbAgentBlocked)
		}
		e := resp["error"].(map[string]any)
		if !strings.Contains(e["message"].(string), "an approval") {
			t.Errorf("refusal did not say what the agent waits on: %v", e["message"])
		}
		hint, _ := e["hint"].(map[string]any)
		if hint == nil || hint["verb"] != "capture-pane" {
			t.Errorf("refusal did not point at capture-pane: %v", e["hint"])
		}
	}

	time.Sleep(300 * time.Millisecond)
	if after := pty.CaptureContent(true, false); after != before || strings.Contains(after, "tuios_blocked_marker") {
		t.Errorf("a refused ask wrote to the pane:\nbefore %q\nafter  %q", before, after)
	}

	// allow_blocked is the caller saying it read the prompt and it takes text.
	res := result(t, c.call(t, `{"id":2,"verb":"ask-agent","params":{"session":"blocked","window":"`+b+`","from":"`+a+`","text":"echo tuios_blocked_marker","allow_blocked":true,"settle":700,"timeout":15000}}`))
	if res["waited_for"] != "needs_input" {
		t.Errorf("waited_for = %v, want needs_input", res["waited_for"])
	}
	if reply, _ := res["reply"].(string); !strings.Contains(reply, "tuios_blocked_marker") {
		t.Errorf("allow_blocked did not type the question: %q", reply)
	}
}

// waitForQuiet waits until the pane has printed nothing for quiet, so a test
// compares screens after the shell has drawn its prompt.
func waitForQuiet(t *testing.T, pty *PTY, quiet, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if last := pty.LastOutput(); last != 0 && time.Since(time.Unix(0, last)) >= quiet {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the pane did not go quiet within %v", limit)
}

// TestAskStopsWaitingWhenTheAgentBlocks covers an agent that goes from working
// to a prompt while an ask waits on it. Waiting on does not clear a prompt, so
// the wait ends with agent_blocked instead of running out the ready timeout.
func TestAskStopsWaitingWhenTheAgentBlocks(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "blocks")
	c := dialVerb(t, sp)

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateWorking}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_, _, _ = sess.ApplyAgentReport(b, AgentReport{State: AgentStateNeedsInput, Message: "which branch?", Kind: "question"})
	}()

	start := time.Now()
	resp := c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"blocks","window":"`+b+`","from":"`+a+`","text":"hello","ready_timeout":20000}}`)
	if code := errCode(t, resp); code != ErrVerbAgentBlocked {
		t.Fatalf("code = %q, want %q", code, ErrVerbAgentBlocked)
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the wait ran on for %v after the agent blocked", waited)
	}
	if msg := errorOf(t, resp)["message"].(string); !strings.Contains(msg, "a question") {
		t.Errorf("refusal did not say the agent waits on a question: %q", msg)
	}
}

// TestBlockedByFollowsTheRuleKind covers blocked_by in list-agents and
// get-agent-state: the kind a report carries while the pane is on
// needs_input, and nothing once it has moved on.
func TestBlockedByFollowsTheRuleKind(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, _, b := twoWindowSession(t, d, "kind")
	c := dialVerb(t, sp)

	read := func() (map[string]any, map[string]any) {
		t.Helper()
		row := result(t, c.call(t, `{"id":1,"verb":"list-agents","params":{"session":"kind"}}`))["agents"].([]any)[0].(map[string]any)
		st := result(t, c.call(t, `{"id":2,"verb":"get-agent-state","params":{"session":"kind","window":"`+b+`"}}`))
		return row, st
	}

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateNeedsInput, Message: "pick one", Kind: "question", Source: AgentSourceScreen}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	row, st := read()
	for name, got := range map[string]map[string]any{"list-agents": row, "get-agent-state": st} {
		if got["blocked_by"] != "question" || got["ready"] != false {
			t.Errorf("%s: blocked_by = %v ready = %v, want question and false", name, got["blocked_by"], got["ready"])
		}
	}

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateIdle}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	row, st = read()
	for name, got := range map[string]map[string]any{"list-agents": row, "get-agent-state": st} {
		if got["blocked_by"] != "" || got["ready"] != true {
			t.Errorf("%s: blocked_by = %v ready = %v after idle, want empty and true", name, got["blocked_by"], got["ready"])
		}
	}
}

// TestAgentKindSurvivesAClientSync covers the merge: a client never sends the
// kind, so a sync that omits it must not wipe it.
func TestAgentKindSurvivesAClientSync(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{{ID: "w", AgentState: AgentStateNeedsInput, AgentKind: "approval", AgentStateAt: 1}}}
	incoming := &SessionState{Windows: []WindowState{{ID: "w"}}}
	retainDaemonExclusive(incoming, canonical)
	if got := incoming.Windows[0].AgentKind; got != "approval" {
		t.Errorf("AgentKind = %q after a client sync, want approval", got)
	}
}

// TestAskReachesARestingAgent drives the whole composition against a plain
// shell, which is the pane that reports nothing: the settle timer is the only
// signal, and the reply is what the pane printed after the question.
func TestAskReachesARestingAgent(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "ask")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"ask","window":"`+b+`","from":"`+a+`","text":"echo tuios_ask_reply","settle":700,"timeout":15000}}`))
	if res["untrusted"] != true {
		t.Error("a reply did not report itself as untrusted")
	}
	if res["settled_by"] != "idle" {
		t.Errorf("settled_by = %v, want idle for a pane that reports no state", res["settled_by"])
	}
	if reply, _ := res["reply"].(string); !strings.Contains(reply, "tuios_ask_reply") {
		t.Errorf("reply did not carry what the pane printed: %q", reply)
	}
}

// TestTailLinesReturnsOnlyWhatCameAfter pins the delta the reply is built from.
func TestTailLinesReturnsOnlyWhatCameAfter(t *testing.T) {
	content := "one\ntwo\nthree\nfour"
	got, truncated := tailLines(content, 2, 10)
	if got != "three\nfour" {
		t.Errorf("tail = %q, want the lines after the baseline", got)
	}
	if truncated {
		t.Error("nothing was cut but truncated was set")
	}
	got, truncated = tailLines(content, 0, 2)
	if got != "three\nfour" || !truncated {
		t.Errorf("capped tail = %q truncated=%v", got, truncated)
	}
	// A baseline past the end of the content is the pane having been cleared,
	// and must not panic or return the whole screen.
	if got, _ = tailLines(content, 99, 10); got != "" {
		t.Errorf("tail past the end = %q, want empty", got)
	}
}

// TestFirstReadIsMarkedNew covers what ReadAt cannot say on the call that sets
// it. A marking read stamps every message it returns, so without this a reader
// could not tell the message that just arrived from the twenty it had already
// seen, and the per-message flag disagreed with the unread count in the footer.
func TestFirstReadIsMarkedNew(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "wasunread")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"wasunread","to":"`+b+`","from":"`+a+`","text":"first"}}`)

	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"wasunread","to":"`+b+`"}}`))
	m := read["messages"].([]any)[0].(map[string]any)
	if m["was_unread"] != true {
		t.Errorf("the first read did not mark the message new: %v", m)
	}
	if m["read_at"] == nil {
		t.Error("the first read did not also stamp read_at")
	}

	read = result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"wasunread","to":"`+b+`"}}`))
	m = read["messages"].([]any)[0].(map[string]any)
	if m["was_unread"] == true {
		t.Errorf("a second read still called the message new: %v", m)
	}
}

// TestUnclaimedPaneReportsNoSource pins the absence. An unset claim reads back
// as "report" because that is the default a caller naming no source gets, so
// listing it verbatim said a pane sitting at a shell prompt had reported itself.
func TestUnclaimedPaneReportsNoSource(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "nosource")
	c := dialVerb(t, sp)

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{State: AgentStateIdle}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}

	res := result(t, c.call(t, `{"id":1,"verb":"list-agents","params":{"session":"nosource","all":true}}`))
	for _, raw := range res["agents"].([]any) {
		row := raw.(map[string]any)
		switch row["window_id"] {
		case a:
			if row["source"] != "" {
				t.Errorf("a pane nothing claimed reported source %v", row["source"])
			}
		case b:
			if row["source"] != "report" {
				t.Errorf("a reporting pane lost its source: %v", row["source"])
			}
		}
	}
}
