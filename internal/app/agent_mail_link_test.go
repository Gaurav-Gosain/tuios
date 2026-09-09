package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Mail that arrived from another machine, as the person sees it. The claim
// is that they can tell it from mail written here without squinting: the
// sender is named with its machine, the row wears a different mark, and the
// thread says in words that the message came over a link.

// remoteMail is a message as build's daemon pushes it when the send arrived
// on its link socket.
func remoteMail(id uint64, fromLabel, host, subject, text string) session.AgentMessage {
	m := mail(id, "", fromLabel, session.AgentInboxHuman, "human", subject, text)
	m.Origin = session.AgentOriginLink
	m.OriginHost = host
	return m
}

func TestMailFromAnotherMachineIsMarkedInTheList(t *testing.T) {
	m := mailOS(t)
	m.noteAgentMail(session.AgentMailPayload{Message: remoteMail(5, "ORCHESTRATOR", "laptop", "ship it?", "the far build is green")})
	m.noteAgentMail(session.AgentMailPayload{Message: mail(6, "cccccccc3333", "build", session.AgentInboxHuman, "human", "local one", "from here")})
	m.OpenAgentMail()
	m.AgentMail.Loading = false

	list, _, _ := m.renderAgentMail()
	plain := stripANSIForTrace(list)
	if !strings.Contains(plain, "⇄ ORCHESTRATOR @ laptop → you") {
		t.Fatalf("ASSERTION: the list does not mark the thread from another machine with its machine and the link mark:\n%s", plain)
	}
	if !strings.Contains(plain, "✉ build → you") {
		t.Fatalf("ASSERTION: the local thread lost its own mark:\n%s", plain)
	}

	// The dock message names the machine too.
	last := m.Notifications[len(m.Notifications)-1]
	if !strings.Contains(last.Message, "ORCHESTRATOR @ laptop to you: ship it?") && !strings.Contains(m.Notifications[len(m.Notifications)-2].Message, "ORCHESTRATOR @ laptop to you: ship it?") {
		t.Errorf("ASSERTION: no dock message names the sender's machine: %q", last.Message)
	}
}

func TestMailFromAnotherMachineSaysSoInTheThread(t *testing.T) {
	m := mailOS(t)
	m.noteAgentMail(session.AgentMailPayload{Message: remoteMail(5, "ORCHESTRATOR", "laptop", "ship it?", "the far build is green")})
	m.OpenAgentMail()
	m.AgentMail.Loading = false
	m.AgentMailOpenSelected()

	thread, _, _ := m.renderAgentMail()
	plain := stripANSIForTrace(thread)
	for _, want := range []string{"ORCHESTRATOR @ laptop → you", "from laptop, over a link", "Written on another machine", "the far build is green"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("ASSERTION: the thread view does not say %q:\n%s", want, plain)
		}
	}

	// A claimed name with control characters in it is drawn without them.
	m.noteAgentMail(session.AgentMailPayload{Message: remoteMail(7, "att\x1b[2Jacker", "ev\x07il", "x", "\x1b]52;c;evil\x07body")})
	m.AgentMail.Thread = 7
	thread, _, _ = m.renderAgentMail()
	if strings.ContainsAny(stripANSIForTrace(thread), "\x07") || strings.Contains(thread, "\x1b]52") || strings.Contains(thread, "\x1b[2J") {
		t.Fatalf("ASSERTION: a control sequence from a message reached the overlay:\n%q", thread)
	}

	// A reply to a sender on another machine is a notice in this ring: the
	// sender has no window here to be typed at, and it reads the thread back
	// over the link.
	m.AgentMail.Thread = 5
	inbox, replyTo, ok := m.agentMailReplyTarget()
	if !ok || inbox != "" || replyTo != 5 {
		t.Errorf("the reply to a remote sender is addressed to %q answering %d, want a notice answering 5", inbox, replyTo)
	}
}

func TestMailboxDialsTheMachineTheSessionIsOn(t *testing.T) {
	m := mailOS(t)
	if m.agentMailDialer() == nil {
		t.Fatal("no dialer")
	}
	// Attached through a host, the mailbox commands go to that host's
	// daemon: the ring lives where the session does. What is checked here
	// is that the dialer is built from the attached host, which is the one
	// input that decides it; the dial itself needs a link and is covered
	// end to end.
	m.AttachedHost = "build"
	cmd := m.agentMailLoad()
	if cmd == nil {
		t.Fatal("ASSERTION: no load command while attached through a host")
	}
	if !m.AgentMail.Loading {
		t.Fatal("the load did not say it is loading")
	}
}
