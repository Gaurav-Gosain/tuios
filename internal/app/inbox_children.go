package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func inboxWithParent(it session.AttentionItem) bool {
	return it.WithParentUntil != 0
}

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

func childrenWaitingWords(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n) + " waiting in children"
}

const ckWithParentVerb = "With the parent agent:"
