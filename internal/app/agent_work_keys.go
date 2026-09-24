package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The keys of the agent review, triage, reply and approval work, and where
// each one lands.
//
// Every one of them returns whether it did anything. A key whose work has not
// landed answers false, and the input path then leaves the key exactly as it
// was before the key was bound: an Inbox key does nothing and records nothing,
// and a rail key on an agent row falls through to the rail's own binding, so r
// still renames and x still opens the destructive menu there until reply and
// cancel are built. The bodies live in review_overlay.go, inbox_lifecycle.go,
// inbox_reply.go and inbox_approvals_ext.go.

// InboxWorkActions are the Inbox actions InboxWorkAction answers.
var InboxWorkActions = map[string]bool{
	config.ActionInboxReview:      true,
	config.ActionInboxSnooze:      true,
	config.ActionInboxUndo:        true,
	config.ActionInboxShowSnoozed: true,
	config.ActionInboxDenyReason:  true,
	config.ActionInboxDetailDown:  true,
	config.ActionInboxDetailUp:    true,
}

// InboxWorkAction runs one of InboxWorkActions on the selected item. handled
// is false when the action did nothing, which is every one of them until its
// work lands.
func (m *OS) InboxWorkAction(action string) (cmd tea.Cmd, handled bool) {
	switch action {
	case config.ActionInboxReview:
		return m.InboxReview()
	case config.ActionInboxSnooze:
		return m.InboxSnooze()
	case config.ActionInboxUndo:
		return m.InboxUndo()
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
	return ok && row.Kind == sidebarRowAgent && row.WindowID != ""
}

// SidebarAgentAction runs one of the agent rows' actions on the row under
// the cursor. handled is false when the action did nothing, and the rail's
// own binding for the key then runs.
func (m *OS) SidebarAgentAction(action string) (cmd tea.Cmd, handled bool) {
	row, ok := m.sidebarCursorRow()
	if !ok || row.Kind != sidebarRowAgent {
		return nil, false
	}
	switch action {
	case config.ActionAgentUnread:
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
