package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// hasWarning reports whether any warning mentions substr.
func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// TestBrowserSessionNamesTheSinksItCannotDeliver checks a served session says
// which alert sinks will not fire, rather than going quiet and letting the user
// conclude agent alerts are broken.
func TestBrowserSessionNamesTheSinksItCannotDeliver(t *testing.T) {
	on := true
	cfg := config.DefaultConfig()
	cfg.Notifications.Agent.Notify = &on
	cfg.Notifications.Agent.Sound = &on
	cfg.Notifications.Agent.SoundMode = "audio"

	got := browserAlertWarnings(cfg)
	if !hasWarning(got, "notify") {
		t.Errorf("nothing said the OSC 9 notification will not arrive: %v", got)
	}
	if !hasWarning(got, "sound") {
		t.Errorf("nothing said the cue plays on the server: %v", got)
	}
}

// TestBrowserWarningsStayQuietForSinksThatWork checks the bell and the dock are
// not reported: BEL reaches the browser and the dock message is drawn in the
// frame, so warning about them would be noise.
func TestBrowserWarningsStayQuietForSinksThatWork(t *testing.T) {
	on, off := true, false
	cfg := config.DefaultConfig()
	cfg.Notifications.Agent.Notify = &off
	cfg.Notifications.Agent.Sound = &on
	cfg.Notifications.Agent.SoundMode = "bell"
	cfg.Notifications.Agent.Dock = &on

	if got := browserAlertWarnings(cfg); len(got) != 0 {
		t.Errorf("warned about sinks a browser can deliver: %v", got)
	}

	// Alerts switched off entirely means nothing to warn about either.
	cfg.Notifications.Agent.Enabled = &off
	cfg.Notifications.Agent.Notify = &on
	cfg.Notifications.Agent.SoundMode = "audio"
	if got := browserAlertWarnings(cfg); len(got) != 0 {
		t.Errorf("warned although agent alerts are disabled: %v", got)
	}
}

// TestBrowserWarningsReachTheSession checks the warnings are attached where the
// TUI reports them from, so they are actually shown rather than merely computed.
func TestBrowserWarningsReachTheSession(t *testing.T) {
	on := true
	cfg := config.DefaultConfig()
	cfg.Notifications.Agent.Notify = &on

	web := NewOS(OSOptions{UserConfig: cfg, BrowserClient: true})
	if !hasWarning(web.ConfigWarnings, "notify") {
		t.Errorf("a browser session was not told: %v", web.ConfigWarnings)
	}

	// A terminal session delivers OSC 9 perfectly well and must not be warned.
	local := NewOS(OSOptions{UserConfig: cfg})
	if hasWarning(local.ConfigWarnings, "notify") {
		t.Errorf("a terminal session was warned about a sink that works: %v", local.ConfigWarnings)
	}
}
