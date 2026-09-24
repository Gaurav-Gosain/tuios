package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Replying to an agent: a one-line editor under the Inbox or the rail that
// queues a message with queue-prompt, typed the moment the agent comes to
// rest, and the queue's length at the right edge of the agent's rail row.
//
// None of it has landed. The entry points below do nothing yet and answer
// false where they are asked, so r on a finished item still says it replies
// to mail, and r and x on an agent row still rename and open the menu.

// inboxReplyAgent opens the reply editor for a finished or errored item. ok
// is false when it did not, and InboxReply then answers as it always has.
func (m *OS) inboxReplyAgent(it session.AttentionItem) (cmd tea.Cmd, ok bool) {
	return nil, false
}

// SidebarAgentReply opens the reply editor for a rail agent row's pane.
func (m *OS) SidebarAgentReply(session, window string) (tea.Cmd, bool) {
	return nil, false
}

// SidebarAgentCancelQueued drops the newest queued message of a rail agent
// row's pane.
func (m *OS) SidebarAgentCancelQueued(session, window string) (tea.Cmd, bool) {
	return nil, false
}

// sidebarAgentQueuedFigure is what a rail agent row shows at its right edge
// in place of the elapsed time while messages wait in the pane's queue, empty
// for none. It draws nothing yet.
func (m *OS) sidebarAgentQueuedFigure(e sidebarAgentEntry) string {
	return ""
}
