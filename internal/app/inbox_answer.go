package app

type answerStep uint8

const (
	answerIdle answerStep = iota
	answerArmed
	answerConfirm
	answerPeekAsk
	answerReason
)

var answerStepNames = [...]string{"idle", "armed", "confirm", "peek ask", "reason"}

type inboxAnswer struct {
	step    answerStep
	armed   inboxArmed
	confirm *inboxConfirm
	peekAsk *inboxPeekAsk
	reason  *inboxReasonLine
}

func (m *OS) answerArm(a inboxArmed) {
	m.setAnswer(inboxAnswer{step: answerArmed, armed: a}, "first press of "+a.key)
}

func (m *OS) answerAsk(c *inboxConfirm) {
	m.setAnswer(inboxAnswer{step: answerConfirm, confirm: c}, "ask "+c.decision)
}

func (m *OS) answerPeekAsk(p *inboxPeekAsk) {
	m.setAnswer(inboxAnswer{step: answerPeekAsk, peekAsk: p}, "peek ask "+p.action)
}

func (m *OS) answerReason(r *inboxReasonLine) {
	m.setAnswer(inboxAnswer{step: answerReason, reason: r}, "reason line")
}

func (m *OS) answerEnd(s answerStep, cause string) {
	if m.Inbox.approvals.answer.step == s {
		m.setAnswer(inboxAnswer{}, cause)
	}
}

func (m *OS) setAnswer(next inboxAnswer, cause string) {
	if was := m.Inbox.approvals.answer.step; was != next.step && verboseLog() {
		m.LogInfo("answer: %s to %s (%s)", answerStepNames[was], answerStepNames[next.step], cause)
	}
	m.Inbox.approvals.answer = next
}
