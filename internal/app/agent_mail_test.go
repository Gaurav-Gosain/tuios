package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The mailbox on the client. These are the claims a person can check on
// screen: mail arrives and is announced, the rail counts it, the overlay
// reads it, and a reply goes back out. Every route in is proved at the level
// that dispatches it, not by the presence of an entry in a table.

// mail is a message as the daemon would push it.
func mail(id uint64, from, fromLabel, to, toLabel, subject, text string) session.AgentMessage {
	kind := "message"
	if to == "" {
		kind = "notice"
	}
	return session.AgentMessage{
		ID: id, Kind: kind, From: from, FromLabel: fromLabel, To: to, ToLabel: toLabel,
		Subject: subject, Text: text, ThreadID: id, SentAt: 1,
	}
}

// TestMailboxKeepsTheIdleTickIdle: a mirror full of mail costs the
// maintenance tick nothing. The tick must not scan it.
func TestMailboxKeepsTheIdleTickIdle(t *testing.T) {
	m := idleOS(t, 3)
	for i := uint64(1); i <= 50; i++ {
		m.noteAgentMail(session.AgentMailPayload{Message: mail(i, "a", "a", "b", "b", "s", "t")})
	}
	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work0, _ := m.TickStats()
	for range 50 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work1, _ := m.TickStats()
	if work1 != work0 {
		t.Errorf("the idle tick did %d units of work with mail in the mirror, want 0", work1-work0)
	}
}

// TestUnverifiedHumanMailIsNotDrawnAsThePerson: a message from human that the
// daemon could not match to an attached client is named as unverified, so the
// person does not read something else's words as their own reply.
func TestUnverifiedHumanMailIsNotDrawnAsThePerson(t *testing.T) {
	m := mail(3, session.AgentInboxHuman, "human", "cccccccc3333", "build", "", "approved")
	m.VerifiedHuman = true
	if got := agentMailSender(m); got != "you" {
		t.Errorf("a verified reply is drawn as %q, want you", got)
	}
	m.VerifiedHuman, m.ClaimedHuman = false, true
	if got := agentMailSender(m); got != "you (unverified)" {
		t.Errorf("a claimed reply is drawn as %q, want you (unverified)", got)
	}
}
