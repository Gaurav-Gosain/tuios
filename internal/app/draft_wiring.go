package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// The wiring every draft to an agent gets, whichever editor holds it: the
// Inbox reply, the bulk reply, and the mail editor's new message and thread
// reply. Each gets the image previewer (inbox_reply_preview.go), the @ list
// (inbox_reply_mention.go) and the target's history (inbox_reply_history.go),
// and the code that serves them asks openDraft for the one open now, so a
// draft opened anywhere behaves as the Inbox reply does.

// draftWires is what an editor keeps beside its composer for the wiring.
type draftWires struct {
	// mentions is the @ list's completer.
	mentions *replyMentions
	// histSession and histWindow are the pane the history is kept for. An
	// empty histWindow keeps none: a bulk reply, or a mail reply to a
	// sender on another machine.
	histSession, histWindow string
	// historyAsked says the history was asked for, once per editor.
	historyAsked bool
	// at is where the draft's first line was drawn, relative to the panel
	// it is in, for its pictures and its caret. ok says every line was
	// drawn, which the pictures need. drawn says the editor is on screen,
	// and top is its first line drawn: a squeezed editor leaves out the
	// lines above it, and the caret is only on screen at or below it.
	at struct {
		x, y  int
		ok    bool
		drawn bool
		top   int
	}
}

// wireDraft gives comp, a draft to the pane sessionID/windowID, what the
// Inbox reply has. history says whether the draft reads and keeps the pane's
// history. A key that did not come from the keyboard opening it marks it at
// once.
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

// openDraft is the draft that takes keys now.
type openDraft struct {
	comp  *composer.Model
	wires *draftWires
	// kind is the overlay the draft is drawn in.
	kind string
}

// openDraft is the draft that takes keys now. The mailbox takes the keyboard
// while it is up (see input/keyboard_wm.go), so its draft comes before the
// Inbox reply's: mail can open over a reply that is still open.
func (m *OS) openDraft() (openDraft, bool) {
	st := &m.AgentMail
	if m.ShowAgentMail && st.Composing && st.Comp != nil {
		return openDraft{comp: st.Comp, wires: &st.wires, kind: "agentmail"}, true
	}
	if ed := m.Inbox.reply.editor; ed != nil && m.ShowInbox {
		return openDraft{comp: ed.comp, wires: &ed.draftWires, kind: "inbox"}, true
	}
	return openDraft{}, false
}

// overlayOnTop reports whether the panel of kind is drawn and no panel is
// drawn over it, so its pictures may go on the host.
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
