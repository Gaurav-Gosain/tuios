package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Safer approvals in the Inbox: a risky approval that needs a second press to
// allow, a deny that carries a reason, and a plan shown whole with its own
// group and keys. The daemon enforces the second press and the plan digest
// (risk_ack and plan_sha); this is the side the person sees.
//
// None of it has landed. The entry points and the render hooks below do
// nothing yet, so the Inbox draws and answers exactly as it did.

// InboxDenyReason opens the reason line for a deny of the selected approval
// or plan (n).
func (m *OS) InboxDenyReason() (tea.Cmd, bool) {
	return nil, false
}

// InboxDetailScroll scrolls the detail under the list, such as a long plan,
// by delta lines.
func (m *OS) InboxDetailScroll(delta int) (tea.Cmd, bool) {
	return nil, false
}

// inboxRowExtras is what an item's row says before its summary on top of what
// the Inbox always says, such as "risky: ". Empty for nothing.
func (m *OS) inboxRowExtras(it session.AttentionItem) string {
	return ""
}

// inboxDetailExtras is the detail under the list and the key hints for an
// item the review, triage and approval work draws: a plan, a risky approval,
// a finished turn's recap. ok is false for every other item, whose detail and
// hints stay the Inbox's own.
func (m *OS) inboxDetailExtras(it session.AttentionItem) (detail func(width int) []string, hints []overlay.Hint, ok bool) {
	return nil, nil, false
}
