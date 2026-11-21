package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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

	m.noteAgentMail(session.AgentMailPayload{Message: remoteMail(7, "att\x1b[2Jacker", "ev\x07il", "x", "\x1b]52;c;evil\x07body")})
	m.AgentMail.Thread = 7
	thread, _, _ := m.renderAgentMail()
	if strings.ContainsAny(stripANSIForTrace(thread), "\x07") || strings.Contains(thread, "\x1b]52") || strings.Contains(thread, "\x1b[2J") {
		t.Fatalf("ASSERTION: a control sequence from a message reached the overlay:\n%q", thread)
	}

	m.AgentMail.Thread = 5
	inbox, replyTo, ok := m.agentMailReplyTarget()
	if !ok || inbox != "" || replyTo != 5 {
		t.Errorf("the reply to a remote sender is addressed to %q answering %d, want a notice answering 5", inbox, replyTo)
	}
}
