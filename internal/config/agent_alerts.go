package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AgentAlertsConfig is the [notifications.agent] table: what tuios does when a
// pane's agent state changes.
//
// Every toggle is a pointer so nil can mean "unset, use the default" and an
// explicit false survives a reload, matching [appearance.sidebar]. The defaults
// are deliberately quiet: an agent that flips between working and idle all day
// is the reason people mute their tools, so only the two states that mean the
// machine has stopped and is waiting on a human raise anything.
type AgentAlertsConfig struct {
	// Enabled is the master switch. False silences every sink below, including
	// the command. Default: true.
	Enabled *bool `toml:"enabled"`

	// States selects which transitions alert at all.
	States AgentAlertStates `toml:"states"`

	// Notify writes an in-band desktop notification to the terminal the client
	// is attached to. Default: true.
	Notify *bool `toml:"notify"`

	// Sound writes a BEL to the same terminal, which decides for itself whether
	// that is audible, a flash, or nothing. Default: false.
	Sound *bool `toml:"sound"`

	// Dock shows the message in tuios's own dock, where it is clickable and
	// jumps to the pane that raised it. Default: true.
	Dock *bool `toml:"dock"`

	// Command is a shell command run on an alert, shorthand for registering one
	// under the after-agent-state hook. Default: empty (nothing runs).
	Command string `toml:"command"`

	// SettleSeconds holds an alert for this long and drops it if the pane leaves
	// the state before it expires, so a flapping agent produces one alert rather
	// than a stream. Zero alerts immediately. Default: 2.
	SettleSeconds *int `toml:"settle_seconds"`

	// SuppressFocused drops alerts for the pane the user is already looking at,
	// for the states that merely report progress. needs_input and errored ignore
	// it: the pane being on screen is not evidence anyone read it. Default: true.
	SuppressFocused *bool `toml:"suppress_focused"`

	// QuietHours silences every sink inside a local-time window written
	// "HH:MM-HH:MM". A window that wraps midnight is understood. Default: empty
	// (never quiet).
	QuietHours string `toml:"quiet_hours"`
}

// AgentAlertStates is the [notifications.agent.states] table: one toggle per
// agent state, naming the states worth interrupting someone for.
type AgentAlertStates struct {
	// NeedsInput is the agent blocked on the user. Default: true.
	NeedsInput *bool `toml:"needs_input"`
	// Errored is the agent having stopped on an error. Default: true.
	Errored *bool `toml:"errored"`
	// Done is the agent having finished its task. Default: false.
	Done *bool `toml:"done"`
	// Idle is the agent having gone quiet, which the stall timer also produces
	// from a guess. Default: false.
	Idle *bool `toml:"idle"`
	// Working is the agent starting work, which is not news. Default: false.
	Working *bool `toml:"working"`
}

// AgentAlertPolicy is AgentAlertsConfig with every default resolved and the
// quiet-hours string parsed, so the hot path is field reads and integer
// comparisons. Build it with ResolveAgentAlerts.
type AgentAlertPolicy struct {
	Enabled         bool
	Notify          bool
	Sound           bool
	Dock            bool
	SuppressFocused bool
	Settle          time.Duration

	// states maps an agent-state wire name to whether it alerts. A name that is
	// absent does not alert, so a state added later is silent until someone opts
	// in rather than noisy on upgrade.
	states map[string]bool

	// quietFrom and quietTo are minutes since local midnight. Equal values mean
	// no quiet window.
	quietFrom, quietTo int
}

// AgentAlertStateNames lists the states the [notifications.agent.states] table
// accepts, in the order they are documented.
var AgentAlertStateNames = []string{"needs_input", "errored", "done", "idle", "working"}

// ResolveAgentAlerts turns the config table into a policy, applying every
// default. A nil receiver resolves to the defaults, so a caller with no config
// at all still gets the documented behavior.
func ResolveAgentAlerts(c *AgentAlertsConfig) AgentAlertPolicy {
	p := AgentAlertPolicy{
		Enabled:         true,
		Notify:          true,
		Sound:           false,
		Dock:            true,
		SuppressFocused: true,
		Settle:          2 * time.Second,
		states: map[string]bool{
			"needs_input": true,
			"errored":     true,
			"done":        false,
			"idle":        false,
			"working":     false,
		},
	}
	if c == nil {
		return p
	}
	boolOr := func(v *bool, def bool) bool {
		if v == nil {
			return def
		}
		return *v
	}
	p.Enabled = boolOr(c.Enabled, p.Enabled)
	p.Notify = boolOr(c.Notify, p.Notify)
	p.Sound = boolOr(c.Sound, p.Sound)
	p.Dock = boolOr(c.Dock, p.Dock)
	p.SuppressFocused = boolOr(c.SuppressFocused, p.SuppressFocused)
	p.states["needs_input"] = boolOr(c.States.NeedsInput, p.states["needs_input"])
	p.states["errored"] = boolOr(c.States.Errored, p.states["errored"])
	p.states["done"] = boolOr(c.States.Done, p.states["done"])
	p.states["idle"] = boolOr(c.States.Idle, p.states["idle"])
	p.states["working"] = boolOr(c.States.Working, p.states["working"])
	if c.SettleSeconds != nil && *c.SettleSeconds >= 0 {
		p.Settle = time.Duration(*c.SettleSeconds) * time.Second
	}
	if from, to, err := ParseQuietHours(c.QuietHours); err == nil {
		p.quietFrom, p.quietTo = from, to
	}
	return p
}

// Alerts reports whether a transition into state is one the user asked to hear
// about. It is the only place the state toggles are read.
func (p AgentAlertPolicy) Alerts(state string) bool {
	return p.Enabled && p.states[state]
}

// IgnoresFocus reports whether an alert for state is raised even for the pane
// the user is looking at. Being on screen says the pane is visible, not that
// anyone read it, so the two states that mean "stopped, waiting on you" are
// raised anyway; the rest are progress reports and are suppressed.
func (p AgentAlertPolicy) IgnoresFocus(state string) bool {
	return state == "needs_input" || state == "errored"
}

// Quiet reports whether now falls inside the configured quiet-hours window. A
// window that wraps midnight ("22:00-08:00") is the union of the two halves.
func (p AgentAlertPolicy) Quiet(now time.Time) bool {
	if p.quietFrom == p.quietTo {
		return false
	}
	m := now.Hour()*60 + now.Minute()
	if p.quietFrom < p.quietTo {
		return m >= p.quietFrom && m < p.quietTo
	}
	return m >= p.quietFrom || m < p.quietTo
}

// ParseQuietHours parses "HH:MM-HH:MM" into minutes since local midnight. An
// empty string is not an error and yields an empty window (0, 0), which Quiet
// reads as "never quiet".
func ParseQuietHours(s string) (from, to int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	start, end, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("want HH:MM-HH:MM, got %q", s)
	}
	if from, err = parseClock(start); err != nil {
		return 0, 0, err
	}
	if to, err = parseClock(end); err != nil {
		return 0, 0, err
	}
	return from, to, nil
}

// parseClock parses one "HH:MM" into minutes since midnight.
func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("want HH:MM, got %q", s)
	}
	hours, err := strconv.Atoi(h)
	if err != nil || hours < 0 || hours > 23 {
		return 0, fmt.Errorf("hour out of range in %q", s)
	}
	mins, err := strconv.Atoi(m)
	if err != nil || mins < 0 || mins > 59 {
		return 0, fmt.Errorf("minute out of range in %q", s)
	}
	return hours*60 + mins, nil
}
