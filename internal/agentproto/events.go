package agentproto

import (
	"context"
	"sync"
)

// What an agent shows, whatever protocol it speaks. The two clients translate
// their protocol's messages into these, and the Session renders and reports
// them.

// Event is one thing the agent showed.
type Event interface{ isEvent() }

// Text is a piece of the agent's reply, or of its reasoning when Thought is
// set. Pieces of one message arrive in order and are shown as they come.
type Text struct {
	Text    string
	Thought bool
}

// Tool is a tool call as it stands now: each event carries everything known
// about the call so far, so a renderer never has to merge.
type Tool struct {
	ID    string
	Title string
	// Kind is what the call does, in the protocol's words: execute, edit,
	// read, fetch, and so on.
	Kind string
	// Status is one of the Tool status constants.
	Status string
	Diffs  []Diff
	// Text is text the call carries, such as a command's output.
	Text string
	// Terminal says the call has a terminal the client was not given, whose
	// output is therefore not shown.
	Terminal bool
	// Input holds the call's raw input as the agent sent it, when it did:
	// each string it holds, by key. It is what decides whether the title is
	// the whole call (see InboxLine).
	Input map[string]string
	// OtherInput says the raw input held something that is not a string, or
	// could not be read.
	OtherInput bool
}

// Tool statuses.
const (
	ToolPending = "pending"
	ToolRunning = "running"
	ToolDone    = "done"
	ToolFailed  = "failed"
)

// Diff is one file a call changes. ACP sends the old and new text, Codex a
// unified diff; exactly one form is set.
type Diff struct {
	Path string
	// Old is the file before, nil for a new file. New is the file after.
	Old *string
	New string
	// Unified is a unified diff, as Codex sends it.
	Unified string
}

// Plan is the agent's plan as it stands now.
type Plan struct {
	Entries []PlanEntry
}

// PlanEntry is one step of a plan. Status is pending, in_progress or
// completed.
type PlanEntry struct {
	Content string
	Status  string
}

// Notice is something the client itself has to say: an error from the agent,
// a retry, a request it refused.
type Notice struct {
	Text  string
	Error bool
}

// Usage is what the agent said about its model, its context window and what
// the conversation cost so far. Every field is optional: a zero field, or a
// false Has flag, is one the agent did not state, and it changes nothing.
type Usage struct {
	// Model is the model now answering, when the agent named it.
	Model string
	// ContextUsed is the tokens in the context window, of ContextSize. The
	// context is stated only when ContextSize is above zero.
	ContextUsed, ContextSize int64
	// Cost is the conversation's cost so far, in Currency (USD when empty),
	// stated when HasCost is set.
	Cost     float64
	HasCost  bool
	Currency string
}

func (Text) isEvent()        {}
func (Usage) isEvent()       {}
func (Tool) isEvent()        {}
func (Plan) isEvent()        {}
func (Notice) isEvent()      {}
func (*Permission) isEvent() {}

// Decisions an option maps to, the words the daemon's request-approval uses.
// An option with no decision can only be picked in the pane.
const (
	DecisionOnce   = "once"
	DecisionAlways = "always"
	DecisionDeny   = "deny"
)

// Option is one answer to a permission request.
type Option struct {
	Label string
	// Decision is what the option means as an Inbox decision, empty when it
	// is not one the Inbox offers.
	Decision string
}

// Permission is the agent asking before a tool call. It is answered exactly
// once, by Choose or Cancel; later answers are dropped.
type Permission struct {
	Tool    Tool
	Options []Option
	// Reason is the agent's explanation, when it gave one.
	Reason string
	// Detail is a line of context the pane shows under the title, such as the
	// directory a command runs in.
	Detail string

	once   sync.Once
	choose func(i int)
	cancel func()
}

// NewPermission builds a permission whose answers call choose and cancel.
func NewPermission(tool Tool, options []Option, choose func(int), cancel func()) *Permission {
	return &Permission{Tool: tool, Options: options, choose: choose, cancel: cancel}
}

// Choose answers with option i. An index out of range is ignored.
func (p *Permission) Choose(i int) bool {
	if i < 0 || i >= len(p.Options) {
		return false
	}
	answered := false
	p.once.Do(func() {
		answered = true
		p.choose(i)
	})
	return answered
}

// Cancel answers that the turn is being cancelled, which approves nothing.
func (p *Permission) Cancel() {
	p.once.Do(func() { p.cancel() })
}

// Pick is the index of the option with decision, or -1.
func (p *Permission) Pick(decision string) int {
	for i, o := range p.Options {
		if decision != "" && o.Decision == decision {
			return i
		}
	}
	return -1
}

// How a turn ended.
const (
	StopFinished  = "finished"
	StopCancelled = "cancelled"
	StopFailed    = "failed"
	StopRefused   = "refused"
	StopLimit     = "limit"
)

// TurnResult is how a turn ended, with the agent's words for it in Detail.
type TurnResult struct {
	Stop   string
	Detail string
}

// Info is what the agent said about itself when the session started.
type Info struct {
	// Agent names the agent, when it said.
	Agent string
	// Session is the protocol's id for the conversation.
	Session string
	// Model is the model, when the agent said.
	Model string
}

// Agent is a started agent process, spoken to over its protocol.
type Agent interface {
	// Start initialises the protocol and opens a conversation in cwd.
	Start(ctx context.Context, cwd string) (Info, error)
	// Prompt runs one turn and returns when it ends. Events arrive on the
	// emit function the client was made with while it runs.
	Prompt(ctx context.Context, text string) (TurnResult, error)
	// Cancel asks the agent to stop the turn that is running.
	Cancel()
	// Done is closed when the agent's connection ends.
	Done() <-chan struct{}
}
