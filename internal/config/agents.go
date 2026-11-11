package config

import (
	"strconv"
	"strings"
	"time"
)

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
	// Mail is the [agents.mail] table: how tuios tells an agent that mail
	// waits for it. See agents_work.go.
	Mail MailConfig `toml:"mail,omitempty"`
	// Checkpoints is the [agents.checkpoints] table: the state of a pane's
	// git work tree saved at the end of each agent turn. See agents_work.go.
	Checkpoints CheckpointsConfig `toml:"checkpoints,omitempty"`
	// HerdrProtocol says which panes are told about the socket tuios accepts
	// herdr's pane state protocol on, which Crush and other harnesses report
	// to by themselves: "always" (the default) for every pane, the way herdr
	// tells every pane, so a harness started from a shell reports too;
	// "agents" for a pane that starts such a harness directly; and "off" for
	// none. A pane told about it reads as a herdr pane to anything that checks
	// HERDR_ENV, herdr itself included, which refuses to start inside one
	// unless its own config allows nesting. See docs/AGENT_STATE.md.
	HerdrProtocol string `toml:"herdr_protocol,omitempty"`
	// Presets are the [agents.presets.NAME] tables: a named start-agent,
	// for start-agent --preset NAME. See AgentPreset.
	Presets map[string]AgentPreset `toml:"presets,omitempty"`
	// HostProgramStatus says whether tuios reports its panes' agent states to
	// the terminal it runs in, with OSC 7501 (the Program Status Protocol):
	// "auto" (the default) asks the terminal at start and reports only when
	// it answers, and "off" never asks. See docs/PROGRAM_STATUS.md.
	HostProgramStatus string `toml:"host_program_status,omitempty"`
	// SilentAfter is how long a pane whose hooks reported working may go
	// without another report before it shows as unknown with the reason
	// silent: a Go duration ("10m"). Empty means DefaultSilentAfter, and "0"
	// or "off" turns it off. See SilentAfterDuration.
	SilentAfter string `toml:"silent_after,omitempty"`
	// FoldWorking folds the agents view's Working group until the person
	// opens it. Nil means off. A fold or an open the person made with F or a
	// click wins over it, and is kept in the client's state file.
	FoldWorking *bool `toml:"fold_working,omitempty"`
	// ViewList is how the agents view shows its list of agents on a screen
	// wide enough for two columns: "column" (the default) keeps the list
	// column beside the focus, "strip" folds the list into a row of chips
	// above the focus, and "auto" folds it only for two agents or fewer. The
	// list key (\ by default) switches it, and the choice is kept in the
	// client's state file, where it wins over this value. See ViewListMode.
	ViewList string `toml:"view_list,omitempty"`
	// ListWidth is the width of the agents view's list column: a number of
	// cells ("52") or a share of the view ("30%"). Empty keeps the width
	// that follows the screen: 38, 48 or 56 cells. < and > and a drag on
	// the edge of the list change it, and that width is kept in the
	// client's state file, where it wins over this value. The list keeps 28
	// cells and the focus 40. See ListWidthCells.
	ListWidth string `toml:"list_width,omitempty"`
	// ReplyRows is the most rows a multi-line editor grows to before it
	// scrolls: the reply to an agent, the reply to many, a mail draft and a
	// new agent's task. Zero keeps each editor's own: 8 for a reply, 6 for a
	// mail draft, 3 for a task. An editor is held to the rows its view has
	// whatever this says, and alt+m in it takes all of them. See
	// ReplyRowsOr.
	ReplyRows int `toml:"reply_rows,omitempty"`
	// AskWaitSeconds is how long ask-human waits for the person's answer
	// when the caller names no timeout. The question stays after the wait.
	// Zero uses 120.
	AskWaitSeconds int `toml:"ask_wait_seconds,omitempty"`
	// UndoWindowSeconds is how long after a dismiss or a snooze in the Inbox,
	// or a queued reply, u takes it back. The daemon and the client read it
	// both. Zero uses 10.
	UndoWindowSeconds int `toml:"undo_window_seconds,omitempty"`
}

// ReplyRowsMax bounds reply_rows. No view is taller.
const ReplyRowsMax = 200

// ReplyRowsOr is reply_rows, or def when it is unset.
func (a AgentsConfig) ReplyRowsOr(def int) int {
	if a.ReplyRows <= 0 {
		return def
	}
	return min(a.ReplyRows, ReplyRowsMax)
}

// ListWidthCells is list_width for a view total cells wide, and 0 when it
// is empty or does not parse.
func (a AgentsConfig) ListWidthCells(total int) int {
	v := strings.TrimSpace(a.ListWidth)
	pct := strings.HasSuffix(v, "%")
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(v, "%")))
	if err != nil || n <= 0 {
		return 0
	}
	if pct {
		return total * min(n, 100) / 100
	}
	return n
}

// DefaultSilentAfter is the default of silent_after. A hooked harness reports
// at every prompt and every tool call, so the longest quiet stretch of a
// healthy turn is one tool call or one model reply. Claude Code stops a Bash
// command after ten minutes at most unless told otherwise, which makes ten
// minutes the first point at which silence says more about the harness than
// about the work.
const DefaultSilentAfter = 10 * time.Minute

// SilentAfterDuration is SilentAfter with its default applied, and zero when
// it is off. A value that does not parse reads as the default, with a
// warning from validation.
func (a AgentsConfig) SilentAfterDuration() time.Duration {
	v := strings.ToLower(strings.TrimSpace(a.SilentAfter))
	if v == "off" {
		return 0
	}
	if v == "" {
		return DefaultSilentAfter
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return DefaultSilentAfter
	}
	return max(d, 0)
}

// On reports whether the agent features are on. They are unless the file
// says enabled = false.
func (a AgentsConfig) On() bool { return a.Enabled == nil || *a.Enabled }

// FoldWorkingOn reports whether the agents view folds Working by default.
func (a AgentsConfig) FoldWorkingOn() bool { return a.FoldWorking != nil && *a.FoldWorking }

// The values of [agents] view_list.
const (
	AgentsViewListColumn = "column"
	AgentsViewListStrip  = "strip"
	AgentsViewListAuto   = "auto"
)

// AgentsViewLists is every value [agents] view_list accepts.
var AgentsViewLists = []string{AgentsViewListColumn, AgentsViewListStrip, AgentsViewListAuto}

// NormalizeAgentsViewList reads an [agents] view_list value. Empty and
// anything unrecognised mean the default, "column".
func NormalizeAgentsViewList(v string) string {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case AgentsViewListStrip, AgentsViewListAuto:
		return v
	}
	return AgentsViewListColumn
}

// ViewListMode is view_list with its default applied.
func (a AgentsConfig) ViewListMode() string { return NormalizeAgentsViewList(a.ViewList) }

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

// The values of [agents] host_program_status.
const (
	HostProgramStatusAuto = "auto"
	HostProgramStatusOff  = "off"
)

// NormalizeHostProgramStatus reads an [agents] host_program_status value.
// Empty and anything unrecognised mean the default, "auto".
func NormalizeHostProgramStatus(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == HostProgramStatusOff {
		return HostProgramStatusOff
	}
	return HostProgramStatusAuto
}

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

// AgentPreset is one [agents.presets.NAME] table: what start-agent --preset
// NAME starts. A flag the caller passes wins over the preset's value; args
// and env are the preset's, then the caller's. The session, folder, name,
// task and parent are the caller's alone.
type AgentPreset struct {
	// Agent is the harness or program, as start-agent AGENT takes it.
	Agent string `toml:"agent,omitempty"`
	// Args go after the harness's own flags and before the caller's.
	Args []string `toml:"args,omitempty"`
	// AddDirs are more directories the agent may work in, each passed with
	// the harness's add-dir flag ([launch] add_dir_flag). ~ is the home
	// folder of the machine the agent starts on.
	AddDirs []string `toml:"add_dirs,omitempty"`
	// Grants are what the agent may do through tuios, as --grants takes them.
	// A preset never grants more than its caller may give.
	Grants []string `toml:"grants,omitempty"`
	// Model, Effort and PermissionMode are the --model, --effort and
	// --permission-mode choices.
	Model          string `toml:"model,omitempty"`
	Effort         string `toml:"effort,omitempty"`
	PermissionMode string `toml:"permission_mode,omitempty"`
	// Env are variables for the agent, each NAME=VALUE. The caller's own
	// --env wins for a name both set.
	Env []string `toml:"env,omitempty"`
	// AfterReady is sent to the agent once it is ready, step by step: a key
	// name (Enter, Escape, Tab, C-c) is that key, anything else is typed as
	// text. It is sent before --prompt.
	AfterReady []string `toml:"after_ready,omitempty"`
	// ReadyTimeout is --ready-timeout, in milliseconds.
	ReadyTimeout int `toml:"ready_timeout,omitempty"`
}
