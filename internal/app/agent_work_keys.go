package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// PrefixWorkActions are the prefix actions PrefixWorkAction answers.
var PrefixWorkActions = map[string]bool{
	config.ActionPrefixReview:       true,
	config.ActionPrefixNextFinished: true,
}

// PrefixWorkAction runs one of PrefixWorkActions. handled is false when the
// action did nothing: ctrl+b O before an agent has been seen, and ctrl+b v on
// a daemon without review-diff. The prefix path then does what it does for an
// unbound key, so in terminal mode the key reaches the focused pane, and the
// repeat window is not armed.
func (m *OS) PrefixWorkAction(action string) (cmd tea.Cmd, handled bool) {
	switch action {
	case config.ActionPrefixReview:
		return m.ReviewFocusedPane()
	case config.ActionPrefixNextFinished:
		return m.JumpToNewestFinished()
	}
	return nil, false
}

// InboxWorkActions are the Inbox actions InboxWorkAction answers.
var InboxWorkActions = map[string]bool{
	config.ActionInboxReview:      true,
	config.ActionInboxSnooze:      true,
	config.ActionInboxUndo:        true,
	config.ActionInboxSettle:      true,
	config.ActionInboxShowSnoozed: true,
	config.ActionInboxDenyReason:  true,
	config.ActionInboxDetailDown:  true,
	config.ActionInboxDetailUp:    true,
}

// InboxWorkAction runs one of InboxWorkActions on the selected item. handled
// is false when the action did nothing: with nothing selected, on an item
// the action does not apply to, or on a daemon without the verb it needs.
func (m *OS) InboxWorkAction(action string) (cmd tea.Cmd, handled bool) {
	switch action {
	case config.ActionInboxReview:
		return m.InboxReview()
	case config.ActionInboxSnooze:
		return m.InboxSnooze()
	case config.ActionInboxUndo:
		return m.InboxUndo()
	case config.ActionInboxSettle:
		return m.InboxSettle()
	case config.ActionInboxShowSnoozed:
		return m.InboxToggleSnoozed()
	case config.ActionInboxDenyReason:
		return m.InboxDenyReason()
	case config.ActionInboxDetailDown:
		return m.InboxDetailScroll(1)
	case config.ActionInboxDetailUp:
		return m.InboxDetailScroll(-1)
	}
	return nil, false
}

// SidebarCursorOnAgent reports whether the rail's keyboard cursor is on a row
// of the agents section. It is the gate before the agent rows' own keys are
// consulted; a cursor anywhere else answers false and the rail's own binding
// runs untouched.
func (m *OS) SidebarCursorOnAgent() bool {
	if !m.SidebarFocused {
		return false
	}
	row, ok := m.sidebarCursorRow()
	return ok && (row.Kind == sidebarRowAgent || row.Kind == sidebarRowSubagentToggle) && row.WindowID != ""
}

// SidebarAgentAction runs one of the agent rows' actions on the row under
// the cursor. handled is false when the action did nothing, and the rail's
// own binding for the key then runs.
func (m *OS) SidebarAgentAction(action string) (cmd tea.Cmd, handled bool) {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return nil, false
	}
	if action == config.ActionAgentSubagents && (row.Kind == sidebarRowAgent || row.Kind == sidebarRowSubagentToggle) {
		return nil, m.SidebarToggleSubagents(row.SessionID, row.WindowID)
	}
	if row.Kind != sidebarRowAgent {
		return nil, false
	}
	switch action {
	case config.ActionAgentUnread:
		if cmd, ok := m.sidebarAgentUndoDrop(row.SessionID, row.WindowID); ok {
			return cmd, true
		}
		return m.SidebarAgentUnread(row.SessionID, row.WindowID)
	case config.ActionAgentSnooze:
		return m.SidebarAgentSnooze(row.SessionID, row.WindowID)
	case config.ActionAgentReply:
		return m.SidebarAgentReply(row.SessionID, row.WindowID)
	case config.ActionAgentReview:
		return m.SidebarAgentReview(row.SessionID, row.WindowID)
	case config.ActionAgentCancelQueued:
		return m.SidebarAgentCancelQueued(row.SessionID, row.WindowID)
	}
	return nil, false
}
