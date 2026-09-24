package session

import (
	"encoding/json"
	"slices"
)

// An agent pane's activity: the prompts, tool calls, tool results and
// finished turns its hooks reported, kept in a bounded ring per pane in daemon
// memory, and the recap computed from it.
//
// The ring is written only by the pane's own reports: activity rides
// set-agent-state, which is scopeSelf and passes the identity guard first. It
// is display only, and nothing reads it to decide a state, a wait or an alert.
// agent-activity reads it: scopeRead with fan-group reach, and a link needs
// list. Its text is the agent's, cleaned by attentionText and marked
// untrusted.

// agentActivityMaxLimit bounds one agent-activity answer.
const agentActivityMaxLimit = 256

// agentActivityParams are what agent-activity takes.
type agentActivityParams struct {
	Session  string `json:"session"`
	Window   string `json:"window"`
	Since    int64  `json:"since"`
	SinceSeq uint64 `json:"since_seq"`
	Limit    int    `json:"limit"`
	Recap    bool   `json:"recap"`
}

// verbAgentActivity answers agent-activity.
func (d *Daemon) verbAgentActivity(_ *connState, params json.RawMessage) (any, *verbError) {
	var p agentActivityParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Limit < 0 || p.Limit > agentActivityMaxLimit {
		return nil, invalidParam("limit", "limit is 1 to 256 entries, or 0 for the default")
	}
	return nil, notBuilt("agent-activity")
}

// AgentActivityReport is set-agent-state's activity parameter: one hook event
// of the pane's own agent.
type AgentActivityReport struct {
	// Event is what happened, one of activityEvents.
	Event string `json:"event"`
	// Tool and Target name the tool and what it acted on, for a tool event.
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	// Text is a prompt's first line, a failure, or what a turn ended with.
	Text string `json:"text,omitempty"`
	// Files are the files a tool call wrote.
	Files []string `json:"files,omitempty"`
	// OK says whether a finished tool call succeeded, nil when unknown.
	OK *bool `json:"ok,omitempty"`
	// Model is the model the harness named, when it did.
	Model string `json:"model,omitempty"`
}

// checkActivityReport refuses an activity whose event is not one of
// activityEvents. A report without one is not checked.
func checkActivityReport(a *AgentActivityReport) *verbError {
	if a == nil {
		return nil
	}
	if !slices.Contains(activityEvents, a.Event) {
		return invalidParam("activity", "activity.event is one of the activity events", activityEvents...)
	}
	return nil
}
