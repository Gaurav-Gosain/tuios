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

	// Sound makes the alert audible. What that means is SoundMode's job.
	// Default: false.
	Sound *bool `toml:"sound"`

	// SoundMode chooses how Sound makes a noise: "audio" plays one of tuios's
	// two cues through whatever audio player the machine has, "bell" writes a
	// BEL and lets the terminal decide what that means, and "both" does each.
	// An unrecognised value is reported as a config warning and read as the
	// default. Default: "audio".
	SoundMode string `toml:"sound_mode"`

	// SoundCooldownSeconds is the shortest gap between two audible cues,
	// counted across every pane. It exists because a workspace where six agents
	// finish together should make one sound rather than six. It does not apply
	// to the bell, which the terminal rate-limits or does not, as it prefers.
	// Default: 3.
	SoundCooldownSeconds *int `toml:"sound_cooldown_seconds"`

	// Sounds replaces the built-in cues with files of the user's own.
	Sounds AgentAlertSounds `toml:"sounds"`

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
	// which is what tuios did before any of this was configurable. Set it false
	// to be told anyway, on the grounds that a pane being on screen is not
	// evidence anyone read it. Default: true.
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
	// Done is the agent having finished its task, which only an explicit report
	// produces. Default: true.
	Done *bool `toml:"done"`
	// Idle is the agent having gone quiet. The stall timer guesses this one from
	// silence, so it is the flappy state and the one that would make tuios the
	// thing people mute. Default: false.
	Idle *bool `toml:"idle"`
	// Working is the agent starting work, which is not news. Default: false.
	Working *bool `toml:"working"`
}

// AgentAlertSounds is the [notifications.agent.sounds] table: a path per cue.
//
// There are two cues rather than five states, because a pair has to be told
// apart by ear in under half a second and a third would only be guessed at.
// needs_input and errored share the attention cue; done and idle share the
// other. A path that does not exist falls back to the built-in cue, so a typo
// costs the custom sound rather than all sound.
type AgentAlertSounds struct {
	// Done is the cue for an agent that stopped. Default: built in.
	Done string `toml:"done"`
	// NeedsInput is the cue for an agent waiting on a human, or one that
	// failed. Default: built in.
	NeedsInput string `toml:"needs_input"`
}

// AgentSoundMode is how an audible alert is made audible.
type AgentSoundMode string

const (
	// AgentSoundAudio plays a cue through a system audio player.
	AgentSoundAudio AgentSoundMode = "audio"
	// AgentSoundBell writes a BEL and lets the terminal decide.
	AgentSoundBell AgentSoundMode = "bell"
	// AgentSoundBoth does each, for a machine where either might be missed.
	AgentSoundBoth AgentSoundMode = "both"
)

// AgentSoundModeNames lists the accepted sound_mode values, in the order they
// are documented.
var AgentSoundModeNames = []string{
	string(AgentSoundAudio), string(AgentSoundBell), string(AgentSoundBoth),
}

// ParseAgentSoundMode resolves a config value, reporting whether it was one of
// the accepted names. An empty value is accepted as the default.
func ParseAgentSoundMode(s string) (AgentSoundMode, bool) {
	switch AgentSoundMode(strings.TrimSpace(s)) {
	case "":
		return defaultAgentSoundMode, true
	case AgentSoundAudio:
		return AgentSoundAudio, true
	case AgentSoundBell:
		return AgentSoundBell, true
	case AgentSoundBoth:
		return AgentSoundBoth, true
	default:
		return defaultAgentSoundMode, false
	}
}

// defaultAgentSoundMode is what sound = true means when nothing says otherwise.
//
// It is audio rather than bell because a BEL on a modern terminal with default
// settings is usually nothing at all, and a user who asked for sound and got
// silence has been told the feature works when it does not. "bell" restores the
// old behaviour in one line.
const defaultAgentSoundMode = AgentSoundAudio

// defaultAgentSoundCooldown is the gap between two audible cues. Long enough
// that a workspace finishing at once makes one sound, short enough that two
// genuinely separate events a few seconds apart are both heard.
const defaultAgentSoundCooldown = 3 * time.Second

// AgentAlertPolicy is AgentAlertsConfig with every default resolved and the
// quiet-hours string parsed, so the hot path is field reads and integer
// comparisons. Build it with ResolveAgentAlerts.
type AgentAlertPolicy struct {
	Enabled         bool
	Notify          bool
	Sound           bool
	SoundMode       AgentSoundMode
	SoundCooldown   time.Duration
	SoundDone       string
	SoundNeedsInput string
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
		SoundMode:       defaultAgentSoundMode,
		SoundCooldown:   defaultAgentSoundCooldown,
		Dock:            true,
		SuppressFocused: true,
		Settle:          2 * time.Second,
		states: map[string]bool{
			"needs_input": true,
			"errored":     true,
			"done":        true,
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
	if mode, ok := ParseAgentSoundMode(c.SoundMode); ok {
		p.SoundMode = mode
	}
	if c.SoundCooldownSeconds != nil && *c.SoundCooldownSeconds >= 0 {
		p.SoundCooldown = time.Duration(*c.SoundCooldownSeconds) * time.Second
	}
	p.SoundDone = strings.TrimSpace(c.Sounds.Done)
	p.SoundNeedsInput = strings.TrimSpace(c.Sounds.NeedsInput)
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

// PlaysAudio reports whether an alert should play a cue through an audio
// player.
func (p AgentAlertPolicy) PlaysAudio() bool {
	return p.Sound && (p.SoundMode == AgentSoundAudio || p.SoundMode == AgentSoundBoth)
}

// PlaysBell reports whether an alert should write a BEL to the terminal.
func (p AgentAlertPolicy) PlaysBell() bool {
	return p.Sound && (p.SoundMode == AgentSoundBell || p.SoundMode == AgentSoundBoth)
}

// AttentionCue reports whether a transition into state should use the cue that
// asks for a human rather than the one that reports the machine stopped. It is
// the state-to-cue map, kept here so the two callers cannot disagree.
func (p AgentAlertPolicy) AttentionCue(state string) bool {
	return state == "needs_input" || state == "errored"
}

// CueFile is the user's replacement for the cue a transition into state uses,
// or empty for the built-in one.
func (p AgentAlertPolicy) CueFile(state string) string {
	if p.AttentionCue(state) {
		return p.SoundNeedsInput
	}
	return p.SoundDone
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
