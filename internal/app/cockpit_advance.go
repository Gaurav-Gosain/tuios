package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Answer and advance: an answer sent from the agents view moves the cursor to
// the next row that needs the person, so a queue of approvals is answered
// with the answer keys alone and no move between them. The cursor moves only
// when it is still where the answer was sent from, on the row or on its
// agent, whose row moves to Working once the answer lands: a person who moved
// on while the answer was on its way keeps the row they moved to. [agents]
// answer_advance = false keeps the cursor on the agent answered. Either way
// the success note says how many more need the person.

// ckAnswerFrom is where an answer was sent from: the item answered, and the
// row and agent under the cursor then.
type ckAnswerFrom struct {
	requestID string
	row       string
	agent     string
}

// ckAnswerFromNow is where an answer to it is sent from now. A row and agent
// are named only while the agents view is open.
func (m *OS) ckAnswerFromNow(it session.AttentionItem) ckAnswerFrom {
	from := ckAnswerFrom{requestID: it.RequestID}
	if m.ShowInbox {
		from.row, from.agent = m.Inbox.ck.key, m.Inbox.ck.akey
	}
	return from
}

// cockpitAnswerAdvance takes an answer that landed: it moves the cursor to the
// next row that needs the person, when the cursor is still where the answer
// was sent from and the setting is on, and returns how many rows need the
// person besides the one answered. It does nothing outside the agents view.
func (m *OS) cockpitAnswerAdvance(from ckAnswerFrom) int {
	if !m.ShowInbox || from.row == "" {
		return 0
	}
	rows := m.cockpitRows()
	ck := &m.Inbox.ck
	var next []int
	for i, r := range rows {
		if r.group != ckNeeds || !r.selectable() {
			continue
		}
		if r.hasItem && r.item.RequestID != "" {
			// An item answered, now or before, waits only for the daemon
			// to take its row away.
			if r.item.RequestID == from.requestID || m.Inbox.replied[r.item.RequestID] {
				continue
			}
		} else if from.agent != "" && r.agent >= 0 && agentKey(ck.agents[r.agent]) == from.agent {
			continue
		}
		next = append(next, i)
	}
	if len(next) == 0 {
		return 0
	}
	still := ck.key == from.row || (from.agent != "" && ck.akey == from.agent)
	if !still || m.UserConfig == nil || !m.UserConfig.Agents.AnswerAdvanceOn() {
		return len(next)
	}
	// The first row after the cursor, else the first: the list is read top
	// down, and a row above was passed over on purpose.
	pick := next[0]
	for _, i := range next {
		if i > ck.idx {
			pick = i
			break
		}
	}
	m.cockpitSetCursor(pick)
	ck.pending = ""
	m.cockpitTouch()
	m.cockpitReplyFollow()
	m.cockpitSync()
	return len(next)
}

// ckMoreNeedYou is the end of an answer's note: how many more rows need the
// person, "" when none do.
func ckMoreNeedYou(n int) string {
	if n <= 0 {
		return ""
	}
	return ". " + plural.CountAs(n, "more needs you", "more need you")
}
