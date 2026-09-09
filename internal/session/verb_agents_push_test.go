package session

import (
	"strings"
	"testing"
	"time"
)

// attachMailClient attaches a TUI client to a session and returns the channel
// its OnAgentMail handler feeds.
func attachMailClient(t *testing.T, name string) chan AgentMailPayload {
	t.Helper()
	c := NewTUIClient()
	if err := c.Connect("test", 80, 24); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	got := make(chan AgentMailPayload, 8)
	c.OnAgentMail(func(p AgentMailPayload) { got <- p })
	if _, err := c.AttachSession(name, false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Pushes are demuxed by the read loop, which the attach itself does not
	// start; cmd/tuios starts it once the handshake is done, as here.
	c.StartReadLoop()
	return got
}

// TestSendAgentMessagePushesToTheAttachedClient is the delivery half of the
// mailbox surface. The ring is store-and-forward for agents, which poll; an
// attached client is drawn for a person, who does not, so the daemon has to
// hand it the message as it is stored. Without this push the only way a
// person hears about a message is to go and read the ring themselves.
func TestSendAgentMessagePushesToTheAttachedClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "push")
	got := attachMailClient(t, "push")
	c := dialVerb(t, sp)

	sent := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"push","to":"`+b+`","from":"`+a+`","subject":"need a decision","text":"merge A or B?"}}`))
	id := idOf(t, sent, "message_id")

	select {
	case p := <-got:
		m := p.Message
		if m.ID != id {
			t.Errorf("the client was handed message %d, want the one just sent, %d", m.ID, id)
		}
		if m.Text != "merge A or B?" || m.Subject != "need a decision" {
			t.Errorf("the pushed message lost its body: %+v", m)
		}
		if m.From != a || m.To != b || m.ThreadID != id {
			t.Errorf("the pushed message lost its addressing: from %q to %q thread %d", m.From, m.To, m.ThreadID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("send-agent-message stored a message and no attached client was told")
	}
}

// TestAgentMailIsPushedOnlyToTheSessionItIsIn: a client attached to another
// session must not hear a conversation that is not its own.
func TestAgentMailIsPushedOnlyToTheSessionItIsIn(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "here")
	twoWindowSession(t, d, "there")
	elsewhere := attachMailClient(t, "there")
	c := dialVerb(t, sp)

	result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"here","to":"`+b+`","from":"`+a+`","text":"private"}}`))

	select {
	case p := <-elsewhere:
		t.Fatalf("a client attached to another session was handed %+v", p)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestThePersonHasAnAddress is the gap the mailbox had: every party to a
// message was a window, and the person at the client is not one. "human" is
// the reserved inbox that names them. An agent writes to it, the unread count
// is reported, a reply from it is threaded like any other, and it cannot be
// asked because there is no keyboard behind it.
func TestThePersonHasAnAddress(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "addr")
	c := dialVerb(t, sp)

	asked := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"addr","to":"human","from":"`+a+`","subject":"which retry policy?","text":"exponential or fixed?"}}`))
	root := idOf(t, asked, "message_id")
	if asked["to"] != AgentInboxHuman || asked["to_name"] != AgentInboxHuman {
		t.Errorf("a message to the person resolved to %v (%v), want human", asked["to"], asked["to_name"])
	}

	listed := result(t, c.call(t, `{"id":2,"verb":"list-agents","params":{"session":"addr"}}`))
	if listed["human_unread"] != float64(1) {
		t.Errorf("list-agents reports %v unread for the person, want 1", listed["human_unread"])
	}

	// Waiting on the person's inbox works like waiting on any inbox, and it
	// matches the mail already there.
	waiter := dialVerb(t, sp)
	waited := result(t, waiter.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-message","session":"addr","window":"human","timeout":2000}}`))
	if waited["matched"] != true {
		t.Errorf("a wait on the person's inbox did not match the mail already there: %v", waited)
	}

	// The reply comes from human, as the client sends it.
	reply := result(t, c.call(t, `{"id":3,"verb":"send-agent-message","params":{"session":"addr","to":"`+a+`","from":"human","reply_to":`+u(root)+`,"text":"exponential"}}`))
	if got := idOf(t, reply, "thread_id"); got != root {
		t.Errorf("the reply is in thread %d, want %d", got, root)
	}
	if reply["from"] != AgentInboxHuman {
		t.Errorf("the reply is from %v, want human", reply["from"])
	}
	thread := result(t, c.call(t, `{"id":4,"verb":"read-agent-messages","params":{"session":"addr","thread":`+u(root)+`}}`))
	msgs, _ := thread["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("the thread holds %d message(s), want 2", len(msgs))
	}
	if last := msgs[1].(map[string]any); last["from_label"] != AgentInboxHuman {
		t.Errorf("the reply reads back from %v, want human", last["from_label"])
	}

	// Reading the person's inbox marks it, like any inbox, and it is never
	// undeliverable: there is no window to close.
	inbox := result(t, c.call(t, `{"id":5,"verb":"read-agent-messages","params":{"session":"addr","to":"human"}}`))
	msgs, _ = inbox["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("the person's inbox holds %d message(s), want 1", len(msgs))
	}
	if first := msgs[0].(map[string]any); first["undeliverable"] == true {
		t.Error("mail to the person read back undeliverable")
	}
	listed = result(t, c.call(t, `{"id":6,"verb":"list-agents","params":{"session":"addr"}}`))
	if listed["human_unread"] != float64(0) {
		t.Errorf("after a read list-agents reports %v unread for the person, want 0", listed["human_unread"])
	}

	// And the one thing it cannot do, refused with the remedy.
	resp := c.call(t, `{"id":7,"verb":"ask-agent","params":{"session":"addr","window":"human","from":"`+b+`","text":"are you there?"}}`)
	if code := errCode(t, resp); code != ErrVerbNoKeyboard {
		t.Errorf("asking the person was refused with %q, want %q", code, ErrVerbNoKeyboard)
	}
	e := resp["error"].(map[string]any)
	hint, _ := e["hint"].(map[string]any)
	if hint == nil || !strings.Contains(hint["command"].(string), "send-agent-message -w human") {
		t.Errorf("the refusal does not say how to reach the person: %v", e)
	}
}

// TestAReadReceiptReachesTheAttachedClient: the count a client draws beside a
// pane follows the agent actually reading, so a marking read is pushed.
func TestAReadReceiptReachesTheAttachedClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "receipt")
	got := attachMailClient(t, "receipt")
	c := dialVerb(t, sp)

	sent := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"receipt","to":"`+b+`","from":"`+a+`","text":"ping"}}`))
	id := idOf(t, sent, "message_id")
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the message itself was never pushed")
	}

	// A peek marks nothing, so it pushes nothing.
	result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"receipt","to":"`+b+`","peek":true}}`))
	select {
	case m := <-got:
		t.Fatalf("a peek pushed a receipt: %+v", m)
	case <-time.After(300 * time.Millisecond):
	}

	result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"receipt","to":"`+b+`"}}`))
	select {
	case p := <-got:
		if p.Message.ID != 0 {
			t.Fatalf("a receipt arrived carrying a message: %+v", p)
		}
		if len(p.ReadIDs) != 1 || p.ReadIDs[0] != id || p.ReadAt == 0 {
			t.Errorf("the receipt names %v at %d, want message %d and a read time", p.ReadIDs, p.ReadAt, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a marking read pushed no receipt, so the client's unread count is stale for ever")
	}
}
