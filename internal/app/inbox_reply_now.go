package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Send now: alt+enter in the reply editor types the reply into the agent's
// pane at once, even while the agent works, rather than queue it for the next
// rest as enter does. The harness takes it as it takes a line the person types
// into its box: most read it at their next tool boundary. It goes by
// send-text with submit, the prompt path ask-agent and the queue's delivery
// type with: one paste, a wait for it to be taken in, and the harness's own
// submit key, with the pane checked for a prompt again right before that key.
//
// The queue is left as it is. A message already waiting there is still typed
// at the next rest, after this one, so nothing is sent twice: this reply never
// enters the queue, and the queued one is not taken out.
//
// Send now is for one agent on this machine, with text only. send-text takes
// no images and no host, so a reply with an image, a reply to an agent on a
// linked machine and a bulk reply are queued with enter, as before. The bulk
// and linked editors keep alt+enter as a new line.

// replyEnterQueues reports whether enter in the reply editor queues the reply
// rather than send it at once: while the agent works. The footer names the
// button by it, and offers alt+enter only then.
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
		// Not reached: these editors keep alt+enter as a new line.
		return m.InboxReplySend()
	}
	if ed.comp.Empty() {
		ed.comp.SetInfo("The reply is empty. Type a message to send it.")
		return nil
	}
	if ed.comp.Automated() || m.ProcessingRemoteKeys {
		// The same refusal as InboxReplySend: send-text as the person
		// needs a key from the keyboard just as a queued reply does.
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
	call := m.inboxCaller()
	params := map[string]any{"session": ed.session, "window": ed.window, "text": text, "submit": true}
	msg := InboxRepliedMsg{Session: ed.session, Window: ed.window, Who: ed.who, Text: text, Send: st.sendSeq, Now: true}
	return func() tea.Msg {
		_, msg.Err = call("send-text", params, inboxReplyTimeout)
		return msg
	}
}
