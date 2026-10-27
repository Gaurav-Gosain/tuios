package config

import (
	"strings"
	"testing"
	"time"
)

// TestMailAlertsFollowTheAgentTableWhenUnset: a config written before
// [notifications.mail] existed alerts on mail exactly as it did, because every
// key the table leaves out takes the agent table's value.
func TestMailAlertsFollowTheAgentTableWhenUnset(t *testing.T) {
	off, on := false, true
	agent := ResolveAgentAlerts(&AgentAlertsConfig{Notify: &off, Dock: &off, Sound: &on, QuietHours: "22:00-08:00"})

	p := ResolveMailAlerts(nil, agent)
	if p.Enabled != agent.Enabled || p.Notify || p.Dock || !p.Sound || p.BetweenAgents {
		t.Errorf("no mail table resolved to %+v, want the agent policy and between_agents off", p)
	}
	p = ResolveMailAlerts(&MailAlertsConfig{}, agent)
	if p.Notify || p.Dock || !p.Sound {
		t.Errorf("an empty mail table resolved to %+v, want the agent policy", p)
	}

	p = ResolveMailAlerts(&MailAlertsConfig{Notify: &on, Dock: &on, Sound: &off, Enabled: &off, BetweenAgents: &on}, agent)
	if !p.Notify || !p.Dock || p.Sound || p.Enabled || !p.BetweenAgents {
		t.Errorf("the mail table's own keys did not win: %+v", p)
	}
	// Quiet hours stay the agent table's: one clock for every alert.
	if p.quietFrom != agent.quietFrom || p.quietTo != agent.quietTo {
		t.Error("the mail policy lost the agent table's quiet hours")
	}
}

// TestNotificationsAreRegisteredAndValidated: the mail table is settable by
// path like the agent table, and a contradiction inside it is reported. A
// message lifetime is warned about only when tuios cannot use it (#1208): a
// short one is the user's choice, a negative one falls back to the default, a
// huge one is cut to the registry's Max, and the wrong type fails the parse.
// notifications.style takes full or plain.
func TestNotificationsAreRegisteredAndValidated(t *testing.T) {
	cfg := DefaultConfig()
	for _, path := range []string{
		"notifications.mail.enabled", "notifications.mail.notify", "notifications.mail.dock",
		"notifications.mail.sound", "notifications.mail.between_agents",
	} {
		if err := SetOptionValue(cfg, path, "false"); err != nil {
			t.Errorf("set %s: %v", path, err)
		}
	}
	if got, _ := GetOptionValue(DefaultConfig(), "notifications.mail.dock"); got != "" {
		t.Errorf("an unset mail key reads back %q, want empty for unset", got)
	}
	// The empty string clears a following option, back to following. An
	// option that follows nothing still refuses it.
	for _, path := range []string{"notifications.mail.enabled", "notifications.mail.notify", "notifications.mail.dock", "notifications.mail.sound"} {
		if err := SetOptionValue(cfg, path, ""); err != nil {
			t.Errorf("clear %s: %v", path, err)
		}
	}
	if m := cfg.Notifications.Mail; m.Enabled != nil || m.Notify != nil || m.Dock != nil || m.Sound != nil {
		t.Errorf("clearing left the mail keys set: %+v", m)
	}
	if err := SetOptionValue(cfg, "notifications.mail.between_agents", ""); err == nil {
		t.Error("between_agents, which follows nothing, took the empty string")
	}

	on := true
	cfg = DefaultConfig()
	cfg.Notifications.Mail.BetweenAgents = &on
	off := false
	cfg.Notifications.Mail.Enabled = &off
	res := ValidateConfig(cfg)
	found := false
	for _, w := range res.Warnings {
		if w.Field == "notifications.mail" && w.Key == "between_agents" && strings.Contains(w.Message, "does nothing") {
			found = true
		}
	}
	if !found {
		t.Errorf("between_agents with mail alerts off raised no warning: %+v", res.Warnings)
	}

	durations := []struct {
		toml     string
		warnKey  string // empty: no warning expected
		info     time.Duration
		warnings time.Duration
	}{
		{"duration = 1", "", time.Second, 8 * time.Second},
		{"duration = 0", "", 2 * time.Second, 8 * time.Second},
		{"duration = -3", "duration", 2 * time.Second, 8 * time.Second},
		{"duration = 99999999999", "duration", time.Hour, 8 * time.Second},
		{"warning_duration = 1", "", 2 * time.Second, time.Second},
		{"warning_duration = 7200", "warning_duration", 2 * time.Second, time.Hour},
	}
	for _, tc := range durations {
		t.Run(tc.toml, func(t *testing.T) {
			cfg, err := ParseUserConfig([]byte("[notifications]\n" + tc.toml + "\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			for _, w := range ValidateConfig(cfg).Warnings {
				if w.Field == "notifications" {
					got = append(got, w.Key+": "+w.Message)
				}
			}
			switch {
			case tc.warnKey == "" && len(got) > 0:
				t.Errorf("a usable lifetime raised warnings: %q", got)
			case tc.warnKey != "" && (len(got) != 1 || !strings.HasPrefix(got[0], tc.warnKey+": ")):
				t.Errorf("want one warning on %s, got %q", tc.warnKey, got)
			}
			s := DefaultSettings()
			ApplyNotificationConfig(cfg, &s)
			if s.NotificationDuration != tc.info || s.NotificationWarningDuration != tc.warnings {
				t.Errorf("applied info %v warning %v, want %v and %v",
					s.NotificationDuration, s.NotificationWarningDuration, tc.info, tc.warnings)
			}
			if s.NotificationErrorDuration != 15*time.Second {
				t.Errorf("error lifetime moved to %v, want the 15s default", s.NotificationErrorDuration)
			}
		})
	}
	if _, err := ParseUserConfig([]byte("[notifications]\nduration = \"fast\"\n")); err == nil {
		t.Error("a string duration parsed without an error")
	}

	// notifications.style: plain is applied, an unknown value warns and
	// falls back to full.
	for _, tc := range []struct {
		value, want string
		warns       bool
	}{
		{"plain", NotificationStylePlain, false},
		{"full", NotificationStyleFull, false},
		{"quiet", NotificationStyleFull, true},
	} {
		cfg, err := ParseUserConfig([]byte("[notifications]\nstyle = \"" + tc.value + "\"\n"))
		if err != nil {
			t.Fatalf("parse style %q: %v", tc.value, err)
		}
		warned := false
		for _, w := range ValidateConfig(cfg).Warnings {
			warned = warned || (w.Field == "notifications" && w.Key == "style")
		}
		s := DefaultSettings()
		ApplyNotificationConfig(cfg, &s)
		if s.NotificationStyle != tc.want || warned != tc.warns {
			t.Errorf("style %q: applied %q, warned %v; want %q, warned %v", tc.value, s.NotificationStyle, warned, tc.want, tc.warns)
		}
	}
}
