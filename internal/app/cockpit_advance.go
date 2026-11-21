package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type ckAnswerFrom struct {
	requestID string
	row       string
	agent     string
}

func (m *OS) ckAnswerFromNow(it session.AttentionItem) ckAnswerFrom {
	from := ckAnswerFrom{requestID: it.RequestID}
	if m.ShowInbox {
		from.row, from.agent = m.Inbox.ck.key, m.Inbox.ck.akey
	}
	return from
}

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

func ckMoreNeedYou(n int) string {
	if n <= 0 {
		return ""
	}
	return ". " + plural.CountAs(n, "more needs you", "more need you")
}
