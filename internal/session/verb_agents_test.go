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

func TestSendAndReadAgentMessage(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "mail")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"mail","to":"`+b+`","from":"`+a+`","subject":"hi","text":"the suite is green"}}`))
	if res["kind"] != agentMsgDirect {
		t.Errorf("kind = %v, want %s", res["kind"], agentMsgDirect)
	}
	if res["to"] != b || res["from"] != a {
		t.Errorf("addressing did not resolve: %v", res)
	}

	// The recipient reads its own inbox and gets the body, flagged untrusted.
	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"mail","to":"`+b+`","unread":true}}`))
	if read["untrusted"] != true {
		t.Error("a read did not report its content as untrusted")
	}
	msgs, _ := read["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if m["text"] != "the suite is green" || m["subject"] != "hi" {
		t.Errorf("message body did not survive: %v", m)
	}

	// Reading marked it read rather than consuming it: the second unread read is
	// empty, and a full read still shows it.
	again := result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"mail","to":"`+b+`","unread":true}}`))
	if n, _ := again["messages"].([]any); len(n) != 0 {
		t.Errorf("a read message stayed unread: %v", again)
	}
	all := result(t, c.call(t, `{"id":4,"verb":"read-agent-messages","params":{"session":"mail","to":"`+b+`"}}`))
	if n, _ := all["messages"].([]any); len(n) != 1 {
		t.Errorf("reading consumed the message instead of marking it: %v", all)
	}
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

// TestListAgentsFindsAnAgentPane is the discovery half: an agent that reported a
// state is listed with its address, and a plain shell is not.
func TestListAgentsFindsAnAgentPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "who")
	c := dialVerb(t, sp)

	if _, _, err := sess.ApplyAgentReport(b, AgentReport{
		State: AgentStateNeedsInput, Message: "waiting for approval", Harness: "claude-code",
	}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}

	res := result(t, c.call(t, `{"id":1,"verb":"list-agents","params":{"session":"who"}}`))
	agents, _ := res["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("listed %d agents, want the one that reported", len(agents))
	}
	got := agents[0].(map[string]any)
	if got["window_id"] != b || got["state"] != "needs_input" || got["harness_id"] != "claude-code" {
		t.Errorf("agent row is wrong: %v", got)
	}
	if got["ready"] != true {
		t.Error("an agent waiting for input did not read as ready to be asked")
	}

	// Unread mail shows against the pane it is waiting for.
	c.call(t, `{"id":2,"verb":"send-agent-message","params":{"session":"who","to":"`+b+`","from":"`+a+`","text":"ping"}}`)
	res = result(t, c.call(t, `{"id":3,"verb":"list-agents","params":{"session":"who"}}`))
	got = res["agents"].([]any)[0].(map[string]any)
	if got["unread"] != float64(1) {
		t.Errorf("unread = %v, want 1", got["unread"])
	}

	// all includes the pane nothing has identified as an agent.
	res = result(t, c.call(t, `{"id":4,"verb":"list-agents","params":{"session":"who","all":true}}`))
	if n, _ := res["agents"].([]any); len(n) != 2 {
		t.Errorf("all listed %d windows, want 2", len(n))
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

// TestWaitForAgentMessageWakesOnArrival is the no-polling half: the wait is
// blocked on the hub and returns when a send publishes.
func TestWaitForAgentMessageWakesOnArrival(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "wakemail")

	waiter := dialVerb(t, sp)
	waiter.send(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-message","session":"wakemail","window":"`+b+`","timeout":5000}}`)

	sender := dialVerb(t, sp)
	// Give the waiter time to subscribe. The wait re-checks the ring on every
	// event, so a message racing the subscription is still matched; the sleep
	// only makes the test exercise the event path rather than the initial check.
	time.Sleep(150 * time.Millisecond)
	sender.call(t, `{"id":2,"verb":"send-agent-message","params":{"session":"wakemail","to":"`+b+`","from":"`+a+`","subject":"late","text":"after the wait started"}}`)

	res := result(t, waiter.readResp(t))
	if res["matched"] != true || res["subject"] != "late" {
		t.Errorf("wait did not wake on the send: %v", res)
	}
}

func TestWaitForAgentMessageTimesOut(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, _, b := twoWindowSession(t, d, "quiet")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-message","session":"quiet","window":"`+b+`","timeout":200}}`)
	if code := errCode(t, resp); code != ErrVerbTimeout {
		t.Errorf("code = %q, want %q", code, ErrVerbTimeout)
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

// TestKilledSessionDropsItsRing covers the lifetime rule: mail dies with the
// session it was addressed inside.
func TestKilledSessionDropsItsRing(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "ephemeral")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"ephemeral","to":"`+b+`","from":"`+a+`","text":"transient"}}`)
	d.agents.forget("ephemeral")

	res := d.agents.read("ephemeral", readQuery{limit: 10})
	if res.Total != 0 {
		t.Errorf("the ring outlived its session: %d messages", res.Total)
	}
}
