package app

import (
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func (m *OS) sendNowKeyName() string {
	if runtime.GOOS == "darwin" {
		if overlay.UseASCII() {
			if m.KeyboardEnhancementsEnabled {
				return "ctrl+enter"
			}
			return "opt+enter"
		}
		if m.KeyboardEnhancementsEnabled {
			return "⌃" + overlay.EnterGlyph
		}
		return "⌥" + overlay.EnterGlyph
	}
	return "alt+" + overlay.EnterGlyph
}

func replyEnterQueues(state string) bool { return state == "working" }

// InboxReplySendNow types the reply into the agent's pane now. An empty
// reply, a reply a key from send-keys touched, and a reply send-text cannot
// carry stay as InboxReplySend leaves them, with a line that says why.
func (m *OS) InboxReplySendNow() tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	if len(ed.targets) > 0 || ed.host != "" {
		return m.InboxReplySend()
	}
	if ed.comp.Empty() {
		ed.comp.SetInfo("The reply is empty. Type a message to send it.")
		return nil
	}
	if ed.comp.Automated() || m.ProcessingRemoteKeys {
		return m.InboxReplySend()
	}
	if n, _ := ed.comp.Images(); n > 0 {
		ed.comp.SetNotice("Send now takes no images. Press enter to queue the reply.")
		return nil
	}
	if state, seq, _, ok := m.railPane(ed.session, ed.window); ok {
		if drawn, _ := m.railAgentState(ed.window, state, seq); drawn == "needs_input" {
			ed.comp.SetNotice(ed.who + " is waiting on a prompt. Answer it first (" + m.inboxKeyOr(config.ActionInboxPeek, "space") + " to peek).")
			return nil
		}
	}
	parts, ok := ed.comp.SubmitParts()
	if !ok {
		return nil
	}
	text := composer.JoinParts(trimReplyParts(parts))
	if len(text) > inboxReplyMax {
		ed.comp.SetNotice("The message is larger than 16 KB. Make it shorter to send.")
		return nil
	}
	m.Inbox.reply.editor = nil
	m.Inbox.ck.img = nil
	if ed.closeAfter {
		m.CloseInbox()
	}
	st := &m.Inbox.reply
	if st.sending == nil {
		st.sending = make(map[uint64]*composer.Model)
	}
	st.sendSeq++
	st.sending[st.sendSeq] = ed.comp
	m.addEcho("", ed.session, ed.window, ed.who, text, nil, st.sendSeq)
	call := m.queueCaller("")
	params := map[string]any{"session": ed.session, "window": ed.window, "text": text, "submit": true}
	msg := InboxRepliedMsg{Session: ed.session, Window: ed.window, Who: ed.who, Text: text, Send: st.sendSeq, Now: true}
	return func() tea.Msg {
		_, msg.Err = call("send-text", params, inboxReplyTimeout)
		return msg
	}
}
