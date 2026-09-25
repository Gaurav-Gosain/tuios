package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func logOS(entries ...LogMessage) *OS {
	m := &OS{Settings: config.Global}
	m.LogMessages = entries
	return m
}

func logAt(h, mi, s int, level, msg string) LogMessage {
	return LogMessage{Time: time.Date(2026, 9, 18, h, mi, s, 0, time.UTC), Level: level, Message: msg}
}

// TestAnEmptySelectionSaysSoRatherThanCopyingNothing.
func TestAnEmptySelectionSaysSoRatherThanCopyingNothing(t *testing.T) {
	m := logOS(logAt(18, 39, 22, "WARN", "only a warning"))

	if cmd := m.CopyLogErrors(); cmd != nil {
		t.Error("a log with no errors still put something on the clipboard")
	}
	if len(m.Notifications) != 1 {
		t.Fatalf("a log with no errors said nothing: %d notifications", len(m.Notifications))
	}
	if !strings.Contains(m.Notifications[0].Message, "no errors") {
		t.Errorf("the notification is %q, which does not say why nothing was copied", m.Notifications[0].Message)
	}
}
