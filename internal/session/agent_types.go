package session

import (
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// AgentState is the semantic state of an agent (a coding-agent CLI or any other
// long-running process) running in a window's pane. It is daemon-owned per-window
// state: a pane reports its own state through the set-agent-state verb, and the
// daemon syncs it to attached clients alongside the rest of the window state.
//
// The zero value is AgentStateNone, which is also what a pane not running an
// agent reports. Storing none as the empty string keeps it out of serialized
// state entirely (omitempty), so older on-disk state and older clients that never
// heard of agent state read back as none, which is exactly the pre-existing
// behavior.
type AgentState string

const (
	// AgentStateNone is the default: the pane is not running an agent, or is not
	// reporting. It serializes as the empty string so it is omitted from state.
	AgentStateNone AgentState = ""
	// AgentStateWorking means the agent is actively working on a task.
	AgentStateWorking AgentState = "working"
	// AgentStateNeedsInput means the agent is blocked waiting for the user.
	AgentStateNeedsInput AgentState = "needs_input"
	// AgentStateIdle means the agent is not working and not blocked; it is the
	// state the output-stall heuristic assigns to a pane that went quiet.
	AgentStateIdle AgentState = "idle"
	// AgentStateDone means the agent finished its task.
	AgentStateDone AgentState = "done"
	// AgentStateErrored means the agent stopped because of an error.
	AgentStateErrored AgentState = "errored"
	// AgentStateUnknown means an agent is present and nothing says what it is
	// doing. It is what the silence timer writes when the screen tier looked
	// and found nothing, in place of idle: idle says nothing needs you, and a
	// pane that went quiet on a prompt no rule knows would be lying.
	AgentStateUnknown AgentState = "unknown"
)

// agentStateByName maps every accepted wire value to its AgentState. "none" is
// the explicit spelling a caller uses to clear the state; it maps to the empty
// AgentStateNone.
var agentStateByName = map[string]AgentState{
	"none":        AgentStateNone,
	"working":     AgentStateWorking,
	"needs_input": AgentStateNeedsInput,
	"idle":        AgentStateIdle,
	"done":        AgentStateDone,
	"errored":     AgentStateErrored,
	"unknown":     AgentStateUnknown,
}

// AgentStateNames lists the accepted wire values in a stable order, for the
// verb's accepted-value schema and for input validation. It is part of the
// public protocol surface; keep the values stable.
var AgentStateNames = []string{"none", "working", "needs_input", "idle", "done", "errored", "unknown"}

// ParseAgentState resolves a wire value to an AgentState, reporting whether the
// value was one of the accepted names. An empty input is not accepted here: the
// verb requires the caller to name a state, and "none" is the spelling that
// clears it.
func ParseAgentState(s string) (AgentState, bool) {
	if s == "" {
		return AgentStateNone, false
	}
	v, ok := agentStateByName[s]
	return v, ok
}

// Name returns the wire spelling of the state, mapping the empty AgentStateNone
// back to "none" so a reader always gets an explicit value.
func (a AgentState) Name() string {
	if a == AgentStateNone {
		return "none"
	}
	return string(a)
}

// NeedsYou reports whether a person has to act on the pane now. It is the
// question every consumer of agent state is really asking, answered in one
// place so the rail, the alert policy and a script all agree on which states
// mean it: a blocked agent and one that stopped on an error.
func (a AgentState) NeedsYou() bool {
	return a == AgentStateNeedsInput || a == AgentStateErrored
}

// Activity is the coarse reading of a state: what the agent is doing, with the
// reason for a block left to the message. It is the shape the states reduce to
// when a reader wants "working, waiting, or at rest" and not the full enum.
func (a AgentState) Activity() string {
	switch a {
	case AgentStateWorking:
		return "working"
	case AgentStateNeedsInput:
		return "waiting"
	case AgentStateIdle, AgentStateDone, AgentStateErrored:
		return "resting"
	case AgentStateUnknown:
		return "unknown"
	default:
		return "none"
	}
}

// agentBlockedBy is what list-agents and get-agent-state report as blocked_by:
// the recorded kind while the pane is on needs_input, and nothing otherwise. The
// state is checked rather than trusted to have cleared the kind, because the
// detector and the stall timer move a pane off needs_input without going
// through ApplyAgentReport.
func agentBlockedBy(w WindowState) string {
	if w.AgentState != AgentStateNeedsInput {
		return ""
	}
	return w.AgentKind
}

// AgentMetaToken is one key and value a pane reported about its agent.
//
// It rides WindowState, so an older peer that does not know the field drops it
// on decode, and a state with no metadata carries nil, which every reader
// takes as "the pane said nothing".
type AgentMetaToken struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Source names who wrote it ("claude-statusline", "hook"), so a writer can
	// clear its own keys without touching another's. Empty when unstated.
	Source string `json:"source,omitempty"`
	// Expires is when the daemon drops it, as Unix nanoseconds, or 0 for a
	// token that lives until it is cleared or the agent leaves the pane.
	Expires int64 `json:"expires,omitempty"`
}

// AgentAttachment is a reference to something a message points at, never the
// bytes themselves. The queue holds a path and the producer keeps the file.
//
// Copying is the expensive part: a megabyte image sitting in an in-memory ring
// nobody reads is the unbounded-growth problem with a bigger constant. Kitty's
// graphics protocol reached the same conclusion, which is why its t=f and t=s
// media pass a path or a shared-memory name instead of the pixels.
//
// The consequence is stated rather than hidden: the file belongs to the
// producer, the queue never copies it, and a reader that comes late may find it
// gone. Missing records that, resolved at read time rather than trusted from
// send time.
type AgentAttachment struct {
	// Kind is the closed set a reader switches on: image or file. It is small on
	// purpose. Extending it later is adding a name; an untyped blob every reader
	// has to sniff can never be narrowed again.
	Kind string `json:"kind"`
	// Path is absolute and host-local. Sender and reader are both processes on
	// the daemon's host, so a path means the same thing to both.
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	// Stashed says the daemon owns this file rather than the sender: it was put
	// in the session's stash, so it is there until the session ends. It is
	// resolved at send time, when the daemon is already looking at the path, and
	// it exists so a reader can tell the two kinds of reference apart. A sender-
	// owned path may be gone by the time it is read; a stashed one may not.
	Stashed bool `json:"stashed,omitempty"`
}

// AgentMessage is one entry in a session's ring.
type AgentMessage struct {
	ID      uint64 `json:"id"`
	Kind    string `json:"kind"`
	Session string `json:"session"`
	// From is the window id the sender claimed. It is a claim rather than an
	// identity: the daemon's socket carries no per-pane credential, so any
	// process that can open it can say it is any window. The loop guards below
	// are built to stop an accident, not an adversary, and the skill says so.
	From      string `json:"from,omitempty"`
	FromLabel string `json:"from_label,omitempty"`
	// To is the recipient window id, empty for a notice.
	To      string `json:"to,omitempty"`
	ToLabel string `json:"to_label,omitempty"`
	Subject string `json:"subject,omitempty"`
	Text    string `json:"text"`

	// ReplyTo is the id of the message this one answers, zero when it answers
	// nothing. It is what "acked" means between two agents: no transport
	// receipt says anything about whether the other side understood, and a
	// reply does.
	ReplyTo uint64 `json:"reply_to,omitempty"`
	// ThreadID is the id of the message the thread started from, and a message
	// that starts one carries its own id here. It is resolved once, at send
	// time, rather than walked from ReplyTo at read time: the ring is bounded,
	// so a walk would stop finding the root the moment the root aged out, and
	// the same thread would answer to two different ids depending on when it
	// was read.
	//
	// It is not a second namespace. A thread id is a message id, so the number
	// send prints is the number a filter takes.
	ThreadID uint64 `json:"thread_id"`
	// ReplyToMissing records that ReplyTo named a message the ring no longer
	// held when this one was threaded, so the thread was rooted at the parent's
	// own id instead of the parent's thread. Refusing the reply would be worse
	// (a bounded ring forgets, and a reply to something it forgot is still a
	// reply), and silently starting a fresh thread would be worse still: it
	// would lose the one fact the reader wanted. This says which happened.
	ReplyToMissing bool `json:"reply_to_missing,omitempty"`

	Attachments []AgentAttachment `json:"attachments,omitempty"`
	SentAt      int64             `json:"sent_at"`
	// ReadAt is zero while the message is unread. Reading marks rather than
	// consumes: a consumed message leaves nothing behind for a human to look at
	// afterwards, and the cap already bounds the ring.
	ReadAt int64 `json:"read_at,omitempty"`
	// SettledBy is set on an ask record only: which signal ended the wait, as
	// ask-agent reported it ("agent-state", "idle", "timeout", ...).
	SettledBy string `json:"settled_by,omitempty"`
	// Origin is AgentOriginLink when the send arrived on the daemon's link
	// socket, which is to say from another machine, and empty when it came
	// from a process on this one. It is set by the daemon from the connection
	// the send arrived on and never from anything in the request, so a
	// message cannot claim to be local. OriginHost is what the sender said
	// its machine is called: a claim, bounded and shown as one. A reader,
	// human or agent, is told both, because who wrote a message is the
	// first thing that decides how much of it to believe.
	Origin     string `json:"origin,omitempty"`
	OriginHost string `json:"origin_host,omitempty"`
	// VerifiedHuman and ClaimedHuman say how far a message from human can be
	// believed, and at most one is set. VerifiedHuman means the send carried
	// the nonce of a client attached to this session at the time, which is the
	// mail overlay's reply path. ClaimedHuman means the send said from=human
	// and carried no such nonce: anything that can open the socket can do that,
	// so it is a claim. Both are set by the daemon at send time and never taken
	// from the request. Both false on a message not from human, and on one
	// stored by an older daemon, which verified nothing.
	VerifiedHuman bool `json:"verified_human,omitempty"`
	ClaimedHuman  bool `json:"claimed_human,omitempty"`
	// Undeliverable is resolved at read time and means the recipient window is
	// gone. A message is never re-homed onto a new pane that happens to carry
	// the old one's name, because that pane is a different agent holding
	// different context.
	Undeliverable bool `json:"undeliverable,omitempty"`
	// WasUnread means this read is the first one to see the message. It exists
	// because ReadAt cannot answer that question on the call that sets it: a
	// marking read stamps ReadAt before returning, so every message it hands
	// back looks read, and a reader could not tell the one that just arrived
	// from the twenty it had already seen.
	WasUnread bool `json:"was_unread,omitempty"`

	// Held is set on mail from another machine that this machine's link
	// policy (hold_mail) put in the person's inbox instead of the recipient's.
	// HeldFor and HeldForLabel are the window it was addressed to, empty for
	// a notice to the session. Released is set once the person passed it on
	// with release-agent-message, and ReleasedFrom on the copy that was
	// delivered, naming the held message. All are set by the daemon and
	// never taken from a request.
	Held         bool   `json:"held,omitempty"`
	HeldFor      string `json:"held_for,omitempty"`
	HeldForLabel string `json:"held_for_label,omitempty"`
	Released     bool   `json:"released,omitempty"`
	ReleasedFrom uint64 `json:"released_from,omitempty"`
}

// AttentionItem is one thing waiting for the person.
type AttentionItem struct {
	// ID is stable for the item's life and unique across daemon restarts on
	// this machine. It is what dismiss-attention takes.
	ID string `json:"id"`
	// Kind is one of AttentionKindNames.
	Kind string `json:"kind"`
	// Host is the machine the item is on, empty for this one. It is here so a
	// hub can merge items from linked hosts into the same list; this daemon
	// only produces its own.
	Host string `json:"host,omitempty"`
	// Session is the session name, the one every verb addresses it by.
	Session string `json:"session"`
	// Window is the pane the item is about: the blocked or finished agent, or
	// the pane that sent the mail. Empty when there is none.
	Window string `json:"window,omitempty"`
	// Workspace is the pane's workspace when the item last changed.
	Workspace int `json:"workspace,omitempty"`
	// Harness is the harness id, when one is known.
	Harness string `json:"harness,omitempty"`
	// Name is what to call the pane: its name, else its title, else the
	// sender's label for mail.
	Name string `json:"name,omitempty"`
	// Summary is one line: the question a blocked agent asked, the error, the
	// note a finished turn carried, or the mail's subject. Control characters
	// are removed, likely secrets are masked and it is cut to 160 bytes.
	Summary string `json:"summary,omitempty"`
	// Options are the answers reply-approval takes for this item, set only
	// while RequestID is: once, always and deny, or the subset the harness can
	// honour. See approvals.go.
	Options []string `json:"options,omitempty"`
	// RequestID is set while a harness hook is holding its permission prompt
	// for an answer from the Inbox, and names that request to reply-approval.
	// It is cleared when the hold ends, whatever ended it, and the item then
	// stays open as long as the pane is still blocked.
	RequestID string `json:"request_id,omitempty"`
	// AlwaysScope is what answering always allows from now on, one rule per
	// line, set only while RequestID is and Options holds always. A client
	// shows it beside the key; the daemon refuses to offer always without it.
	AlwaysScope []string `json:"always_scope,omitempty"`
	// Expires is when the hold ends, in unix nanoseconds, set with RequestID.
	Expires int64 `json:"expires,omitempty"`
	// Answer and AnsweredBy are set only on the item a close event with reason
	// answered carries: the decision the person made and the client they made
	// it from, so every other client can say it was answered elsewhere.
	Answer     string `json:"answer,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
	// Since is when the item started waiting, in unix nanoseconds. An update
	// keeps it, so the wait time an Inbox row shows is the whole wait.
	Since int64 `json:"since"`
	// Seq is the queue's revision when the item last changed. It only ever
	// goes up, across restarts too.
	Seq uint64 `json:"seq"`
	// Thread is the mail thread, for a mail item.
	Thread uint64 `json:"thread,omitempty"`
	// HeldID is set on a mail item whose newest message is mail from another
	// machine held for the person (hold_mail): the message id
	// release-agent-message takes. HeldFor is the name of the window it was
	// addressed to, empty for a notice to the session.
	HeldID  uint64 `json:"held_id,omitempty"`
	HeldFor string `json:"held_for,omitempty"`
	// ForHost is set on an outbox item: the machine the mail waits for. It is
	// not Host, which marks an item mirrored from another machine; an outbox
	// item is this machine's own.
	ForHost string `json:"for_host,omitempty"`
	// Count is how many unread messages a mail item stands for, or how many
	// turns a finished item stands for.
	Count int `json:"count,omitempty"`
	// CompletionSeq is the pane's completion_seq when a finished item last
	// changed. Focusing the pane at that count or later closes it.
	CompletionSeq uint64 `json:"completion_seq,omitempty"`
	// Closed is the close reason, set only on the item an attention event
	// with action close carries.
	Closed string `json:"closed,omitempty"`
	// Stale is set on an item from another machine whose link is down. The
	// item is what that machine said last, and nobody here can check it now.
	// SeenAt is when this daemon last heard from that machine, in unix
	// nanoseconds. Both are empty for an item of this machine.
	Stale  bool  `json:"stale,omitempty"`
	SeenAt int64 `json:"seen_at,omitempty"`

	// SnoozedUntil is when a snoozed item opens again, in unix nanoseconds,
	// set on an item listed with include_snoozed. -1 means it waits until its
	// fact changes. Zero on an item that is not snoozed.
	SnoozedUntil int64 `json:"snoozed_until,omitempty"`
	// MarkedUnread is set on a finished item the person reopened with mark
	// unread after looking at the pane.
	MarkedUnread bool `json:"marked_unread,omitempty"`
	// Risk names the risk rules an approval's command matched, set on an
	// approval or plan item whose request did. An allow from the Inbox then
	// needs risk_ack naming exactly these. See internal/risk.
	Risk []string `json:"risk,omitempty"`
	// DenyMessage is set when the harness takes a reason with a deny, so the
	// Inbox can offer to type one.
	DenyMessage bool `json:"deny_message,omitempty"`
	// PlanLines and PlanSHA describe a plan item's text, which is served by
	// get-approval rather than carried here: how many lines it has, and the
	// digest an answer names so it applies only to the plan that was shown.
	PlanLines int    `json:"plan_lines,omitempty"`
	PlanSHA   string `json:"plan_sha,omitempty"`

	// remoteSeq is the Seq the machine the item came from gave it, for an
	// item mirrored from a linked host. It orders that machine's changes,
	// which can reach this daemon out of order around a relisting.
	remoteSeq uint64 //nolint:unused // only the full build mirrors items from linked hosts
}

// AgentActivityEntry is one entry of a pane's activity ring.
type AgentActivityEntry struct {
	// Seq numbers the pane's entries from 1. It is per pane and per daemon
	// start.
	Seq uint64 `json:"seq"`
	// At is when the daemon recorded it, in unix nanoseconds.
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	// Tool and Target name a tool call and what it acts on. A command entry
	// carries its command line in Target.
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	// Files are the files a finished tool call wrote.
	Files []string `json:"files,omitempty"`
	// OK says how a tool call ended, nil when nothing said.
	OK *bool `json:"ok,omitempty"`
	// Exit is a command's exit status, nil when the shell sent none.
	Exit *int `json:"exit,omitempty"`
	// Text is a prompt's first line, a failure, the first line a turn ended
	// with, or a state entry's new state.
	Text string `json:"text,omitempty"`

	// turns is how many turns the pane finished with a state entry: its
	// completion_seq delta. The recap adds them up. Never on the wire.
	turns uint64 //nolint:unused // only the full build keeps the activity ring
}

// AgentInboxHuman is the reserved inbox id of the person at the attached
// client. It is the one address in the mailbox that is not a window: a message
// to it is read from the client's mail overlay, and a reply from there is sent
// from it. It cannot be asked, because there is no keyboard behind it.
const AgentInboxHuman = "human"

// AgentHintEnv is the environment variable a wrapper sets to name the harness
// it runs. It is for the agents process detection cannot see: one in a
// container, in a VM, over ssh, or under a build tool such as go run. Nothing
// in the pane's process tree is the agent there, so the one who started the
// wrapper says what it is:
//
//	TUIOS_AGENT=claude-code docker run -it sandbox claude
//
// Only the foreground process group leader's own environment is read, and only
// this one variable of it. A value that names no manifest is ignored.
const AgentHintEnv = "TUIOS_AGENT"

// AgentOriginLink is the Origin of a message that arrived over a link. It is
// the only value Origin takes: a message from this machine has none.
const AgentOriginLink = "link"

// AgentActivityRecap summarises a pane's activity since a time.
type AgentActivityRecap struct {
	// Since is when the recap starts: the time asked for, or the oldest entry
	// the ring still holds when it has dropped entries newer than that.
	Since int64 `json:"since"`
	// Turns is how many turns the pane finished: its completion_seq delta.
	Turns uint64 `json:"turns"`
	// Files are the files tool calls wrote, first written first, at most
	// recapFilesShown of them. FilesTotal counts them all.
	Files      []string `json:"files"`
	FilesTotal int      `json:"files_total"`
	// Commands counts shell tool calls and the shell's own commands.
	Commands int `json:"commands"`
	// Tests is the newest command that reads as a test run, nil when none.
	Tests *AgentActivityTest `json:"tests,omitempty"`
	// LastSaid is the first line the newest turn ended with.
	LastSaid string `json:"last_said,omitempty"`
	// State is the pane's agent state now.
	State string `json:"state"`
}

// PromptPeek is the peek-prompt result as a client decodes it. The daemon
// writes the same fields in peekResult.
type PromptPeek struct {
	Session    string           `json:"session"`
	Window     string           `json:"window"`
	Name       string           `json:"name"`
	Harness    string           `json:"harness"`
	State      string           `json:"state"`
	StateAt    int64            `json:"state_at"`
	WaitingMS  int64            `json:"waiting_ms"`
	Blocked    bool             `json:"blocked"`
	Found      bool             `json:"found"`
	Answerable bool             `json:"answerable"`
	Reason     string           `json:"reason"`
	Source     string           `json:"source"`
	Kind       string           `json:"kind"`
	Message    string           `json:"message"`
	PromptID   string           `json:"prompt_id"`
	Lines      []string         `json:"lines"`
	Options    []harness.Option `json:"options"`
	Actions    []string         `json:"actions"`
}

// PromptResponse is the respond result as a client decodes it.
type PromptResponse struct {
	Window    string `json:"window"`
	Action    string `json:"action"`
	Sent      string `json:"sent"`
	PromptID  string `json:"prompt_id"`
	SettledBy string `json:"settled_by"`
	State     string `json:"state"`
	Message   string `json:"message"`
}

// SubagentsText is how n subagents read on the rail and in the subagents
// key: "1 subagent" or "n subagents", and "" at zero.
func SubagentsText(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 subagent"
	}
	return strconv.Itoa(n) + " subagents"
}

// AgentActivityTest is the newest test run a recap found.
type AgentActivityTest struct {
	Cmdline string `json:"cmdline"`
	// OK is whether it passed, null when nothing said: the tool call has not
	// finished, or the harness and the shell reported no result.
	OK *bool `json:"ok"`
	At int64 `json:"at"`
}

// Offers reports whether the peek lists action among the answers.
func (p *PromptPeek) Offers(action string) bool {
	return slices.Contains(p.Actions, action)
}
