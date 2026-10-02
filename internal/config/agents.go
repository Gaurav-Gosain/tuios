package config

import "strings"

// The [agents] table: how tuios treats the coding agents in its panes.
//
//	[agents.approvals]
//	enabled = ["claude-code", "opencode"]
//	hold_seconds = 120
//
//	[agents.permissions]
//	mode = "strict"
//	grants = ["read", "write", "fan"]
//
// Apart from enabled, it is file-plane config, outside the option registry,
// for the same reason [hosts] is: a list of harness names is not a scalar
// with one settable path. enabled is a plain bool, so it is in the registry
// and on the settings page. The daemon reads the table at start and again
// whenever the file changes.

// AgentsConfig is the [agents] table.
type AgentsConfig struct {
	// Enabled turns every agent feature on or off: agent detection, the
	// agent rows of the rail, the Inbox, agent mail, approvals, attention,
	// the agent keys and start-agent. Nil means on, the default. False keeps
	// only the multiplexer. See On.
	Enabled *bool `toml:"enabled,omitempty"`
	// Approvals is the [agents.approvals] table. See ApprovalsConfig.
	Approvals ApprovalsConfig `toml:"approvals,omitempty"`
	// Permissions is the [agents.permissions] table: what a process in a
	// pane may do through tuios. See pane_grants.go.
	Permissions PermissionsConfig `toml:"permissions,omitempty"`
	// Recap is the [agents.recap] table: the summary of what an agent did
	// while the person was away. See agents_work.go.
	Recap RecapConfig `toml:"recap,omitempty"`
	// Queue is the [agents.queue] table: messages waiting to be typed to an
	// agent when it comes to rest. See agents_work.go.
	Queue QueueConfig `toml:"queue,omitempty"`
	// HerdrProtocol says which panes are told about the socket tuios accepts
	// herdr's pane state protocol on, which Crush and other harnesses report
	// to by themselves: "always" (the default) for every pane, the way herdr
	// tells every pane, so a harness started from a shell reports too;
	// "agents" for a pane that starts such a harness directly; and "off" for
	// none. A pane told about it reads as a herdr pane to anything that checks
	// HERDR_ENV, herdr itself included, which refuses to start inside one
	// unless its own config allows nesting. See docs/AGENT_STATE.md.
	HerdrProtocol string `toml:"herdr_protocol,omitempty"`
}

// On reports whether the agent features are on. They are unless the file
// says enabled = false.
func (a AgentsConfig) On() bool { return a.Enabled == nil || *a.Enabled }

// AgentsOffMessage is the one line a command, a verb or a key prints when it
// is an agent feature and the agent features are off.
const AgentsOffMessage = "Agent features are off. Set agents.enabled = true in the config to use this command."

// AgentsOffVerbMessage is what the daemon answers an agent verb with while
// the agent features are off. A verb can come from the CLI, an MCP client or
// a herdr client, so it names the machine and not a command.
const AgentsOffVerbMessage = "Agent features are off on this machine. Set agents.enabled = true in its config.toml to turn them on."

// The values of [agents] herdr_protocol.
const (
	HerdrProtocolAgents = "agents"
	HerdrProtocolAlways = "always"
	HerdrProtocolOff    = "off"
)

// NormalizeHerdrProtocol reads an [agents] herdr_protocol value. Empty and
// anything unrecognised mean the default, "always".
func NormalizeHerdrProtocol(v string) string {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case HerdrProtocolAgents, HerdrProtocolOff:
		return v
	}
	return HerdrProtocolAlways
}

// ApprovalsConfig is the [agents.approvals] table: which harnesses hand their
// permission prompts to the Inbox, so the person can answer one from wherever
// they are instead of going to the pane.
//
// It is off by default. A harness named here has its approval hook wait, for
// up to HoldSeconds, for an answer from the Inbox. While it waits the harness
// shows no prompt of its own, which is why nothing waits unless asked to. When
// the wait ends with no answer the harness shows its own prompt as before.
type ApprovalsConfig struct {
	// Enabled lists the harnesses whose approvals the Inbox may answer, by
	// harness id or alias (claude, claude-code, opencode, kilo, qwen). Empty, the
	// default, turns the feature off.
	Enabled []string `toml:"enabled,omitempty"`
	// HoldSeconds is how long a hook waits for an answer before it gives the
	// prompt back to the harness. Zero means the default, 120. The daemon
	// keeps it between 10 and 300, and the Claude Code hook tuios installs
	// allows 310 seconds, so a hold never outlives the hook.
	HoldSeconds int `toml:"hold_seconds,omitempty"`
	// HoldPlans also hands a plan an agent in plan mode asks to have
	// approved to the Inbox, for the harnesses Enabled names. Unset means
	// true: a plan follows enabled. See PlansHeld.
	HoldPlans *bool `toml:"hold_plans,omitempty"`
	// Risk is the [agents.approvals.risk] table: the rules that mark an
	// approval risky. See RiskConfig.
	Risk RiskConfig `toml:"risk,omitempty"`
}
