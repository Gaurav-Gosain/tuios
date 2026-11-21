package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

type draftWires struct {
	mentions                *replyMentions
	histSession, histWindow string
	historyAsked            bool
	at                      struct {
		x, y  int
		ok    bool
		drawn bool
		top   int
	}
}

func (m *OS) wireDraft(comp *composer.Model, sessionID, windowID string, history bool) draftWires {
	if m.ProcessingRemoteKeys {
		comp.MarkAutomated()
	}
	comp.SetPreviewer(m.replyPreviewer())
	w := draftWires{mentions: m.newReplyMentions(sessionID, windowID)}
	comp.SetCompleter(w.mentions)
	if history {
		w.histSession, w.histWindow = sessionID, windowID
	}
	return w
}

type openDraft struct {
	comp  *composer.Model
	wires *draftWires
	kind  string
}

func (m *OS) openDraft() (openDraft, bool) {
	st := &m.AgentMail
	if m.ShowAgentMail && st.Composing && st.Comp != nil {
		return openDraft{comp: st.Comp, wires: &st.wires, kind: "agentmail"}, true
	}
	if tc := m.taskCapture; tc != nil && m.TaskCaptureOpen() && tc.field == tcTask {
		return openDraft{comp: tc.ed, wires: &tc.wires, kind: overlayKindTaskCapture}, true
	}
	if r := m.Inbox.approvals.answer.reason; r != nil && r.ed != nil && m.ShowInbox && m.InboxReasonOpen() {
		return openDraft{comp: r.ed, wires: &r.wires, kind: "inbox"}, true
	}
	if sp := m.Inbox.ck.spawn; sp != nil && m.ShowInbox && sp.field == spTask && !sp.list {
		return openDraft{comp: sp.task, wires: &sp.taskWires, kind: "inbox"}, true
	}
	if ed := m.Inbox.reply.editor; ed != nil && m.ShowInbox {
		return openDraft{comp: ed.comp, wires: &ed.draftWires, kind: "inbox"}, true
	}
	return openDraft{}, false
}

func (m *OS) overlayOnTop(kind string) bool {
	h, ok := m.overlayHitByKind(kind)
	if !ok {
		return false
	}
	for _, o := range m.OverlayHits {
		if o.Z > h.Z {
			return false
		}
	}
	return true
}
