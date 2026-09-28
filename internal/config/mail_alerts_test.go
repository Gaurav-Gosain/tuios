package config

import (
	"strings"
	"testing"
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

// TestMailAlertsAreRegisteredAndValidated: the table is settable by path like
// the agent table, and a contradiction inside it is reported.
func TestMailAlertsAreRegisteredAndValidated(t *testing.T) {
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
}
