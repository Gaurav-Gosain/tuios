package config

import (
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestResolveAgentAlertsDefaults(t *testing.T) {
	p := ResolveAgentAlerts(nil)
	if !p.Enabled || !p.Notify || !p.Dock || !p.SuppressFocused {
		t.Fatalf("defaults should enable notify and dock: %+v", p)
	}
	if p.Sound {
		t.Fatal("sound must be off by default")
	}
	if p.Settle != 2*time.Second {
		t.Fatalf("settle = %v, want 2s", p.Settle)
	}
	// The whole point of the defaults: only the states that mean the machine
	// stopped and is waiting on a human.
	for _, state := range []string{"needs_input", "errored"} {
		if !p.Alerts(state) {
			t.Errorf("%s should alert by default", state)
		}
	}
	for _, state := range []string{"done", "idle", "working", "none", ""} {
		if p.Alerts(state) {
			t.Errorf("%s must not alert by default", state)
		}
	}
}

func TestResolveAgentAlertsExplicitFalseSurvives(t *testing.T) {
	no := false
	p := ResolveAgentAlerts(&AgentAlertsConfig{
		States: AgentAlertStates{NeedsInput: &no},
	})
	if p.Alerts("needs_input") {
		t.Fatal("an explicit false must not be re-defaulted to true")
	}
	if !p.Alerts("errored") {
		t.Fatal("turning one state off must not touch the others")
	}
}

func TestAgentAlertsMasterSwitchSilencesEveryState(t *testing.T) {
	no, yes := false, true
	p := ResolveAgentAlerts(&AgentAlertsConfig{
		Enabled: &no,
		States:  AgentAlertStates{NeedsInput: &yes, Errored: &yes, Done: &yes, Idle: &yes, Working: &yes},
	})
	for _, state := range AgentAlertStateNames {
		if p.Alerts(state) {
			t.Errorf("enabled=false must silence %s", state)
		}
	}
}

func TestAgentAlertsIgnoresFocus(t *testing.T) {
	p := ResolveAgentAlerts(nil)
	if !p.IgnoresFocus("needs_input") || !p.IgnoresFocus("errored") {
		t.Fatal("a stopped agent is worth saying even about the visible pane")
	}
	if p.IgnoresFocus("done") || p.IgnoresFocus("idle") {
		t.Fatal("progress reports about the visible pane are suppressible")
	}
}

func TestParseQuietHours(t *testing.T) {
	tests := []struct {
		in       string
		from, to int
		wantErr  bool
	}{
		{in: "", from: 0, to: 0},
		{in: "22:00-08:00", from: 22 * 60, to: 8 * 60},
		{in: " 09:30 - 17:45 ", from: 9*60 + 30, to: 17*60 + 45},
		{in: "22:00", wantErr: true},
		{in: "25:00-08:00", wantErr: true},
		{in: "22:60-08:00", wantErr: true},
		{in: "ten-eleven", wantErr: true},
	}
	for _, tc := range tests {
		from, to, err := ParseQuietHours(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseQuietHours(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if err == nil && (from != tc.from || to != tc.to) {
			t.Errorf("ParseQuietHours(%q) = %d,%d want %d,%d", tc.in, from, to, tc.from, tc.to)
		}
	}
}

func TestAgentAlertsQuietWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 8, 13, h, m, 0, 0, time.Local) }

	wrapping := ResolveAgentAlerts(&AgentAlertsConfig{QuietHours: "22:00-08:00"})
	for _, tc := range []struct {
		t     time.Time
		quiet bool
	}{
		{at(23, 0), true}, {at(3, 0), true}, {at(7, 59), true},
		{at(8, 0), false}, {at(12, 0), false}, {at(21, 59), false},
	} {
		if got := wrapping.Quiet(tc.t); got != tc.quiet {
			t.Errorf("wrapping window at %s = %v, want %v", tc.t.Format("15:04"), got, tc.quiet)
		}
	}

	plain := ResolveAgentAlerts(&AgentAlertsConfig{QuietHours: "09:00-17:00"})
	if plain.Quiet(at(8, 59)) || !plain.Quiet(at(9, 0)) || !plain.Quiet(at(16, 59)) || plain.Quiet(at(17, 0)) {
		t.Fatal("a same-day window should be half-open [from, to)")
	}

	if ResolveAgentAlerts(nil).Quiet(at(3, 0)) {
		t.Fatal("no configured window must never be quiet")
	}
}

func TestAgentAlertsParseFromTOML(t *testing.T) {
	var cfg UserConfig
	src := `
[notifications]
duration = 6

[notifications.agent]
enabled = true
sound = true
settle_seconds = 0
quiet_hours = "23:00-06:00"
command = "notify-send agent"

[notifications.agent.states]
done = true
needs_input = false
`
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	p := ResolveAgentAlerts(&cfg.Notifications.Agent)
	if !p.Sound {
		t.Error("sound = true did not survive the round trip")
	}
	if p.Settle != 0 {
		t.Errorf("settle_seconds = 0 should disable the wait, got %v", p.Settle)
	}
	if !p.Alerts("done") || p.Alerts("needs_input") {
		t.Error("per-state toggles did not survive the round trip")
	}
	if !p.Quiet(time.Date(2026, 8, 13, 1, 0, 0, 0, time.Local)) {
		t.Error("quiet_hours did not survive the round trip")
	}
	if cfg.Notifications.Agent.Command != "notify-send agent" {
		t.Errorf("command = %q", cfg.Notifications.Agent.Command)
	}
	if cfg.Notifications.Duration != 6 {
		t.Error("the nested table displaced an existing key")
	}
}

func TestValidateAgentAlertsWarnsOnBadQuietHours(t *testing.T) {
	bad := -1
	cfg := DefaultConfig()
	cfg.Notifications.Agent.QuietHours = "yesterday"
	cfg.Notifications.Agent.SettleSeconds = &bad
	res := ValidateConfig(cfg)
	if res.HasErrors() {
		t.Fatal("a bad alert setting is a warning, not a fatal error")
	}
	var keys []string
	for _, w := range res.Warnings {
		if w.Field == "notifications.agent" {
			keys = append(keys, w.Key)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("warnings = %v, want quiet_hours and settle_seconds", keys)
	}
}
