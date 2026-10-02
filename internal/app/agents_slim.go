//go:build slim

package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/edition"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// tuios-slim has no Inbox, no agent mail and no agent alerts. These stand in
// for inbox*.go, agent_mail.go, agent_alert.go, agent_work_keys.go,
// agent_integration.go and their render files. agentsSeen is false, so the
// chrome that waits for an agent never shows: the prefix menu's Inbox lines,
// the palette's agent entries and the agent rows of the settings.
//
// A full daemon may still push mail to this client. The update loop takes
// the message as it does in the full build, and the stubs below drop it, so
// the event listener is armed again and keeps running.

// InboxState keeps the fields the core reads. It stays empty.
type InboxState struct {
	Items    []session.AttentionItem
	Live     bool
	Gen      uint64
	Peek     *inboxPeek
	noReview bool
	call     inboxVerbCall
	nonce    func() string
}

// inboxPeek is never open.
type inboxPeek struct {
	Loading      bool
	LoadingSince time.Time
}

// AgentMailState keeps the fields the core reads. It stays empty.
type AgentMailState struct {
	Messages     []session.AgentMessage
	Gen          uint64
	Error        string
	Loading      bool
	LoadingSince time.Time
}

type pendingAgentAlert struct{}

type inboxRow struct {
	item *session.AttentionItem
}

type agentMailThread struct {
	ID      uint64
	From    string
	To      string
	Subject string
	Unread  bool
}

func (m *OS) agentsSeen() bool { return false }

// railListsAgents is false: the rail's agent rows lead to the Inbox, the
// review and agent mail, which tuios-slim leaves out. A full daemon still
// sends agent state, so the rows are left out here.
func railListsAgents() bool { return false }

// noInbox says the Inbox, or agent mail when filter asks for it, is not in
// this build.
func (m *OS) noInbox(filter string) {
	what := "The Inbox"
	if filter == session.AttentionMail {
		what = "Agent mail"
	}
	m.ShowNotification(edition.MissingMessage(what), "info", m.Settings.NotificationDuration)
}

func (m *OS) checkAgentIntegrationCmd() tea.Cmd { return nil }

func (m *OS) OpenAgentMail() tea.Cmd                              { return nil }
func (m *OS) OpenAgentMailForWindow(string) tea.Cmd               { return nil }
func (m *OS) OpenAgentMailThread(uint64) tea.Cmd                  { return nil }
func (m *OS) OpenInbox(filter string)                             { m.noInbox(filter) }
func (m *OS) JumpToNextAttention() tea.Cmd                        { m.noInbox(""); return nil }
func (m *OS) resetAgentMail()                                     {}
func (m *OS) inboxReach(session.AttentionItem) bool               { return false }
func (m *OS) endInboxWatch()                                      {}
func (m *OS) startInboxWatch() tea.Cmd                            { return nil }
func (m *OS) agentMailLoad() tea.Cmd                              { return nil }
func (m *OS) flushDueAgentAlerts(time.Time)                       {}
func (m *OS) AgentMailSelect(int)                                 {}
func (m *OS) AgentMailMove(int)                                   {}
func (m *OS) AgentMailOpenSelected() tea.Cmd                      { return nil }
func (m *OS) CloseAgentMail()                                     { m.ShowAgentMail = false }
func (m *OS) InboxSelect(int)                                     {}
func (m *OS) InboxMove(int)                                       {}
func (m *OS) inboxRows() []inboxRow                               { return nil }
func (m *OS) InboxActivate() tea.Cmd                              { return nil }
func (m *OS) CloseInbox()                                         { m.ShowInbox = false }
func (m *OS) AgentMailUnread() int                                { return 0 }
func (m *OS) agentMailUnreadFor(string) int                       { return 0 }
func (m *OS) agentMailThreads() []agentMailThread                 { return nil }
func (m *OS) considerAgentAlert(*terminal.Window, string, string) {}
func (m *OS) noteAgentReturn(*terminal.Window)                    {}
func (m *OS) paneApprovalWord(_, kind string) string              { return kind }

func (m *OS) sidebarAgentQueuedFigures(sidebarAgentEntry) []string { return nil }

func (m *OS) sidebarHeaderCounts([]sidebarAgentEntry) sidebarAgentCountInfo {
	return sidebarAgentCountInfo{}
}

func (m *OS) mailAlertPolicy() config.MailAlertPolicy {
	return config.MailAlertPolicy{}
}

func (m *OS) renderAgentMail() (string, overlay.Geometry, []overlayRowHit) {
	return "", overlay.Geometry{}, nil
}

func (m *OS) renderInbox() (string, overlay.Geometry, []overlayRowHit) {
	return "", overlay.Geometry{}, nil
}

// PrefixWorkActions are the same keys as in the full build. PrefixWorkAction
// answers false for each, so the key does what an unbound key does.
var PrefixWorkActions = map[string]bool{
	config.ActionPrefixReview:       true,
	config.ActionPrefixNextFinished: true,
}

func (m *OS) PrefixWorkAction(string) (tea.Cmd, bool)   { return nil, false }
func (m *OS) SidebarCursorOnAgent() bool                { return false }
func (m *OS) SidebarAgentAction(string) (tea.Cmd, bool) { return nil, false }
func (m *OS) JumpToNewestFinished() (tea.Cmd, bool)     { return nil, false }
func (m *OS) NotePaneKey()                              {}
func (m *OS) InboxApprovalFetch() tea.Cmd               { return nil }
func (m *OS) InboxRecapFetch() tea.Cmd                  { return nil }
func (m *OS) AgentRecapFetch() tea.Cmd                  { return nil }
func (m *OS) InboxReplyOpen() bool                      { return false }
func (m *OS) InboxReplyType(string)                     {}

// The messages of the Inbox and the mail. Only the mail events can arrive,
// from a full daemon; the rest are never sent in tuios-slim.
type (
	AgentMailMsg struct {
		Payload session.AgentMailPayload
	}
	AgentMailLoadMsg   struct{}
	AgentMailMarkMsg   struct{ Thread uint64 }
	AgentMailLoadedMsg struct{}
	AgentMailSentMsg   struct{ Err error }
	AgentMailMarkedMsg struct{ Err error }

	agentIntegrationMsg        struct{}
	inboxWatchMsg              struct{}
	foreignListingRefreshedMsg struct{}
	InboxAlertDueMsg           struct{}
	InboxDismissedMsg          struct{}
	InboxMarkedMsg             struct{}
	InboxPeekMsg               struct{}
	InboxRespondedMsg          struct{}
	InboxApprovalRepliedMsg    struct{}
	InboxApprovalDetailMsg     struct{}
	InboxRepliedMsg            struct{}
	InboxQueueDroppedMsg       struct{}
	InboxRecapMsg              struct{}
	AgentReturnRecapMsg        struct{}
	InboxAskAnsweredMsg        struct{}
	InboxResumedMsg            struct{}
	InboxReleasedMsg           struct{}
)

func (m *OS) noteAgentMail(session.AgentMailPayload)            {}
func (m *OS) agentMailMarkRead(uint64) tea.Cmd                  { return nil }
func (m *OS) applyAgentMailLoaded(AgentMailLoadedMsg)           {}
func (m *OS) applyAgentMailSent(AgentMailSentMsg)               {}
func (m *OS) takePendingInboxThread() tea.Cmd                   { return nil }
func (m *OS) handleInboxWatch(inboxWatchMsg) tea.Cmd            { return nil }
func (m *OS) applyInboxAlertDue(InboxAlertDueMsg)               {}
func (m *OS) applyInboxDismissed(InboxDismissedMsg)             {}
func (m *OS) applyInboxMarked(InboxMarkedMsg)                   {}
func (m *OS) applyInboxPeek(InboxPeekMsg)                       {}
func (m *OS) applyInboxResponded(InboxRespondedMsg) tea.Cmd     { return nil }
func (m *OS) applyInboxApprovalReplied(InboxApprovalRepliedMsg) {}
func (m *OS) applyInboxApprovalDetail(InboxApprovalDetailMsg)   {}
func (m *OS) applyInboxReplied(InboxRepliedMsg)                 {}
func (m *OS) applyInboxQueueDropped(InboxQueueDroppedMsg)       {}
func (m *OS) applyInboxRecap(InboxRecapMsg)                     {}
func (m *OS) applyAgentReturnRecap(AgentReturnRecapMsg)         {}
func (m *OS) applyInboxAskAnswered(InboxAskAnsweredMsg)         {}
func (m *OS) applyInboxResumed(InboxResumedMsg)                 {}
func (m *OS) applyInboxReleased(InboxReleasedMsg)               {}
