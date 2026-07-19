package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestConfigProblemsAreVisibleOnceTheTUIIsUp covers the reporting path for
// config problems. They were previously handed to tea.Println before the
// program existed, so the returned command was discarded and nothing was ever
// printed; even had it printed, it would have happened before the alternate
// screen was entered and been wiped by the first frame. They have to arrive
// somewhere the user can still read them after startup.
func TestConfigProblemsAreVisibleOnceTheTUIIsUp(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Appearance.BorderStyle = "definitely-not-a-border"

	o := NewOS(OSOptions{UserConfig: cfg})
	if len(o.ConfigWarnings) == 0 {
		t.Fatal("a bad border style produced no config warnings")
	}

	logsBefore := len(o.LogMessages)
	notificationsBefore := len(o.Notifications)
	o.Init()

	if len(o.LogMessages) <= logsBefore {
		t.Error("config problems were not written to the in-app log")
	}
	if len(o.Notifications) <= notificationsBefore {
		t.Error("config problems raised no notification, so nobody would look at the log")
	}

	var found bool
	for _, line := range o.LogMessages[logsBefore:] {
		if strings.Contains(line.Message, "border_style") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the offending key was not named in the log: %+v", o.LogMessages[logsBefore:])
	}
}

// TestACleanConfigSaysNothing keeps the reporting from becoming noise that
// users learn to ignore.
func TestACleanConfigSaysNothing(t *testing.T) {
	o := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	if len(o.ConfigWarnings) != 0 {
		t.Fatalf("the default config warns about itself: %v", o.ConfigWarnings)
	}

	notificationsBefore := len(o.Notifications)
	o.Init()
	if len(o.Notifications) != notificationsBefore {
		t.Error("a clean config raised a notification")
	}
}
