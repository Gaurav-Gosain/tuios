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

// TestCopyingErrorsLeavesOutEverythingElse. The control exists for the paste
// into a bug report, so a warning that the build is a version behind is noise
// there and a plain info line doubly so.
//
// Negative control: copying every level makes this fail on the warning.
func TestCopyingErrorsLeavesOutEverythingElse(t *testing.T) {
	m := logOS(
		logAt(18, 39, 22, "WARN", "Build mismatch: client is ahead"),
		logAt(18, 41, 26, "ERROR", "switch failed: attach failed"),
		logAt(18, 41, 30, "INFO", "Attached session-0"),
	)

	if cmd := m.CopyLogErrors(); cmd == nil {
		t.Fatal("copying errors from a log that holds one produced no command")
	}
	// The notification is the observable half here, and it has to say something
	// happened: a copy that quietly puts nothing on the clipboard cannot be
	// told from one that did not run.
	if len(m.Notifications) != 1 {
		t.Fatalf("copying errors said nothing: %d notifications", len(m.Notifications))
	}
	if !strings.Contains(m.Notifications[0].Message, "Copied") {
		t.Errorf("the notification is %q", m.Notifications[0].Message)
	}
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

// TestAnEmptyLogSaysSo, for the A control, which is reachable before anything
// has been logged at all.
func TestAnEmptyLogSaysSo(t *testing.T) {
	m := logOS()
	if cmd := m.CopyLogs(); cmd != nil {
		t.Error("an empty log put something on the clipboard")
	}
	if len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Message, "nothing in the log") {
		t.Errorf("an empty log did not say so: %+v", m.Notifications)
	}
}

// TestALineCarriesItsTimeAndLevel. What gets pasted has to be readable on its
// own, away from the viewer that formatted it.
func TestALineCarriesItsTimeAndLevel(t *testing.T) {
	got := logLine(logAt(18, 41, 26, "ERROR", "switch failed: attach failed"))
	for _, want := range []string{"18:41:26", "ERROR", "switch failed: attach failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("a copied line is %q, which is missing %q", got, want)
		}
	}
}
