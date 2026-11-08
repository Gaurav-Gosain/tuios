package app

// The answer flow of a held prompt: where an answer from the Inbox or the
// agents view stands between the key and the reply. It is one step at a time:
//
//   - answerArmed: a risky allow was pressed once; the same key again within
//     inboxRiskPressWindow answers, any other key drops it.
//   - answerConfirm: a list key or a card button asked; enter answers, any
//     other key drops it (InboxConfirmKey).
//   - answerPeekAsk: a key in the peek asked; enter answers (InboxPeekAskKey).
//   - answerReason: the say-why line is open; enter rejects with what it holds.
//
// The four were fields of their own, set and cleared by hand at fifteen
// places, so a step could be left standing behind the next: the reason line
// opened over a confirm that the next key then answered. Now the step and its
// data are one value with one writer, setAnswer, and each step replaces the
// one before. internal/lint holds every other write of approvals.answer out
// (TestOnlyInboxAnswerWritesTheAnswerStep).

// answerStep is where an answer stands.
type answerStep uint8

const (
	answerIdle answerStep = iota
	answerArmed
	answerConfirm
	answerPeekAsk
	answerReason
)

// answerStepNames name the steps in the verbose log.
var answerStepNames = [...]string{"idle", "armed", "confirm", "peek ask", "reason"}

// inboxAnswer is the answer step and the data of that step only.
type inboxAnswer struct {
	step    answerStep
	armed   inboxArmed
	confirm *inboxConfirm
	peekAsk *inboxPeekAsk
	reason  *inboxReasonLine
}

// answerArm makes a the step: the first press of a risky allow.
func (m *OS) answerArm(a inboxArmed) {
	m.setAnswer(inboxAnswer{step: answerArmed, armed: a}, "first press of "+a.key)
}

// answerAsk makes c the step: an answer that waits for enter.
func (m *OS) answerAsk(c *inboxConfirm) {
	m.setAnswer(inboxAnswer{step: answerConfirm, confirm: c}, "ask "+c.decision)
}

// answerPeekAsk makes p the step: an answer in the peek that waits for enter.
func (m *OS) answerPeekAsk(p *inboxPeekAsk) {
	m.setAnswer(inboxAnswer{step: answerPeekAsk, peekAsk: p}, "peek ask "+p.action)
}

// answerReason makes r the step: the say-why line.
func (m *OS) answerReason(r *inboxReasonLine) {
	m.setAnswer(inboxAnswer{step: answerReason, reason: r}, "reason line")
}

// answerEnd ends step s, when it is the step now. Another step stands.
func (m *OS) answerEnd(s answerStep, cause string) {
	if m.Inbox.approvals.answer.step == s {
		m.setAnswer(inboxAnswer{}, cause)
	}
}

// setAnswer is the one writer of the answer step. A change is logged at
// verbose level.
func (m *OS) setAnswer(next inboxAnswer, cause string) {
	if was := m.Inbox.approvals.answer.step; was != next.step && verboseLog() {
		m.LogInfo("answer: %s to %s (%s)", answerStepNames[was], answerStepNames[next.step], cause)
	}
	m.Inbox.approvals.answer = next
}
