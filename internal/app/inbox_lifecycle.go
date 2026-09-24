package app

import tea "charm.land/bubbletea/v2"

// The Inbox's lifecycle beyond answering and dismissing: snoozing an item,
// undoing a dismiss or a snooze made a moment ago, showing what is snoozed,
// marking a finished pane unread from the rail, and walking the unseen
// finished turns newest first (ctrl+b O). Each is the person's act, sent with
// mark-attention and the attach nonce.
//
// None of it has landed. The entry points below do nothing yet, and answer
// false where the input path asks, so their keys do what they did before they
// were bound.

// InboxSnooze starts a snooze of the selected item: the footer offers the
// four lengths and the next digit picks one.
func (m *OS) InboxSnooze() (tea.Cmd, bool) {
	return nil, false
}

// InboxUndo reopens the item dismissed or snoozed last, within 10 seconds.
func (m *OS) InboxUndo() (tea.Cmd, bool) {
	return nil, false
}

// InboxToggleSnoozed shows or hides the snoozed items under the list.
func (m *OS) InboxToggleSnoozed() (tea.Cmd, bool) {
	return nil, false
}

// JumpToNewestFinished goes to the newest finished turn nobody has seen, and
// on a repeat within 5 seconds to the next older one (ctrl+b O). handled is
// false while the work has not landed, and the prefix path then treats the
// key as unbound and does not arm the repeat window.
func (m *OS) JumpToNewestFinished() (tea.Cmd, bool) {
	return nil, false
}

// SidebarAgentUnread marks a rail agent row's finished turn unread again.
func (m *OS) SidebarAgentUnread(session, window string) (tea.Cmd, bool) {
	return nil, false
}

// SidebarAgentSnooze snoozes the Inbox item of a rail agent row's pane.
func (m *OS) SidebarAgentSnooze(session, window string) (tea.Cmd, bool) {
	return nil, false
}
