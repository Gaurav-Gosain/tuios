package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Child agents in the Inbox: the client half of attention_children.go in
// internal/session.
//
// A delegated child's question that waits for its parent agent first comes
// with WithParentUntil set. The agents view lists it with the working agents
// and says "With the parent agent", the rail does not count it, and it raises
// no alert. A person who looks can still answer it. When the daemon releases
// it the item moves to Needs you and alerts like a new one, in the attached
// session too, since no agent state moved at the release.
//
// An item about a child agent names its root. The root agent's row counts
// the items that wait for the person in its chain: "2 waiting in children".

// inboxWithParent reports whether an item waits for a parent agent before it
// is the person's.
func inboxWithParent(it session.AttentionItem) bool {
	return it.WithParentUntil != 0
}

// paneWithParent reports whether the pane's question waits for its parent
// agent. The state sync does not alert on such a pane: the Inbox does, when
// the question is released.
func (m *OS) paneWithParent(windowID string) bool {
	if windowID == "" {
		return false
	}
	for _, it := range m.Inbox.Items {
		if it.Host == "" && it.Window == windowID && inboxWithParent(it) {
			return true
		}
	}
	return false
}

// childrenWaiting counts the items in the chain under e that wait for the
// person: approvals, plans and questions of child agents whose root is e.
// Questions still with a parent agent are not counted.
func (m *OS) childrenWaiting(e sidebarAgentEntry) int {
	n := 0
	for _, it := range m.Inbox.Items {
		if it.Root == nil || it.Root.Window != e.WindowID || it.Host != e.Host || it.Stale || inboxWithParent(it) {
			continue
		}
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionAsk:
			n++
		}
	}
	return n
}

// childrenWaitingWords is what a root agent's row says of its chain, empty
// when nothing waits there.
func childrenWaitingWords(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n) + " waiting in children"
}

// ckWithParentVerb fronts the row of a question that waits for the parent
// agent.
const ckWithParentVerb = "With the parent agent:"
