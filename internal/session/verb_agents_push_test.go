package session

import (
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
