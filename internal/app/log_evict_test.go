package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Every message the dock shows is logged at info, so a burst of them must not
// push the one error out of the log: a full log drops its oldest info entry
// first. The second half is the positive one: with nothing but errors, the
// oldest error goes, so the log still holds MaxLogMessages entries.
func TestLogKeepsErrorsThroughAnInfoFlood(t *testing.T) {
	m := notifTestOS(t, 120)
	m.Log("ERROR", "the error")
	for i := range config.MaxLogMessages * 3 {
		m.Log("INFO", "info %d", i)
	}
	if len(m.LogMessages) != config.MaxLogMessages {
		t.Fatalf("the log holds %d entries, want %d", len(m.LogMessages), config.MaxLogMessages)
	}
	if m.LogMessages[0].Message != "the error" {
		t.Fatalf("the error was pushed out by info entries; the oldest entry is %q", m.LogMessages[0].Message)
	}

	for i := range config.MaxLogMessages + 1 {
		m.Log("ERROR", "error %d", i)
	}
	if len(m.LogMessages) != config.MaxLogMessages || m.LogMessages[0].Message == "the error" {
		t.Fatalf("a log of only errors did not drop its oldest: %d entries, oldest %q", len(m.LogMessages), m.LogMessages[0].Message)
	}
}
