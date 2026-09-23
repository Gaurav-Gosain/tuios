package config

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
// It is file-plane config, outside the option registry, for the same reason
// [hosts] is: a list of harness names is not a scalar with one settable path.
// The daemon reads it at start and again whenever the file changes.

// AgentsConfig is the [agents] table.
type AgentsConfig struct {
	// Approvals is the [agents.approvals] table. See ApprovalsConfig.
	Approvals ApprovalsConfig `toml:"approvals,omitempty"`
	// Permissions is the [agents.permissions] table: what a process in a
	// pane may do through tuios. See pane_grants.go.
	Permissions PermissionsConfig `toml:"permissions,omitempty"`
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
	// harness id or alias (claude, claude-code, opencode, kilo). Empty, the
	// default, turns the feature off.
	Enabled []string `toml:"enabled,omitempty"`
	// HoldSeconds is how long a hook waits for an answer before it gives the
	// prompt back to the harness. Zero means the default, 120. The daemon
	// keeps it between 10 and 300, and the Claude Code hook tuios installs
	// allows 310 seconds, so a hold never outlives the hook.
	HoldSeconds int `toml:"hold_seconds,omitempty"`
}
