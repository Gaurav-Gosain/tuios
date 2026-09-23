package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// This file lends the external test package app_test what it needs to drive
// the model through the input package, which package app cannot import.

// InboxOSForTest is inboxOS, focused on its first pane "w-1".
func InboxOSForTest(t *testing.T) *OS {
	t.Helper()
	m := inboxOS(t, zeroSettle())
	m.FocusedWindow = 0
	return m
}

// ApplyInboxEventsForTest folds attention events into the mirror as the
// watcher would.
func ApplyInboxEventsForTest(m *OS, items ...session.AttentionItem) {
	m.applyInboxEvents(opened(items...))
}

// AgePopForTest makes a question that popped count as on screen long ago.
func AgePopForTest(m *OS) {
	m.Inbox.poppedAt = time.Now().Add(-time.Minute)
	m.Inbox.shown.since = time.Now().Add(-time.Minute)
}

// LastNoteForTest is the last notification shown.
func LastNoteForTest(m *OS) string {
	return lastNote(m)
}

// AskItemForTest is askItem.
func AskItemForTest(id, sess, window string, options ...string) session.AttentionItem {
	return askItem(id, sess, window, options...)
}
