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

func TestMailFromAnotherMachineSaysSoInTheThread(t *testing.T) {
	m := mailOS(t)
	m.noteAgentMail(session.AgentMailPayload{Message: remoteMail(5, "ORCHESTRATOR", "laptop", "ship it?", "the far build is green")})
	m.OpenAgentMail()
	m.AgentMail.Loading = false
	m.AgentMailOpenSelected()

	// A claimed name with control characters in it is drawn without them.
	m.noteAgentMail(session.AgentMailPayload{Message: remoteMail(7, "att\x1b[2Jacker", "ev\x07il", "x", "\x1b]52;c;evil\x07body")})
	m.AgentMail.Thread = 7
	thread, _, _ := m.renderAgentMail()
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
