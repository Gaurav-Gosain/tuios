package session

import (
	"cmp"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// Attention kinds. They are wire values: the kind field of an item and the
// kind filter of list-attention.
const (
	AttentionApproval = "approval"
	AttentionQuestion = "question"
	AttentionMail     = "mail"
	AttentionErrored  = "errored"
	AttentionFinished = "finished"
	// AttentionResume is a restored pane whose agent conversation can be
	// resumed. See agent_resume.go.
	AttentionResume = "resume"
	// AttentionOutbox is mail this machine holds for another machine whose
	// link is down, and deliveries that machine refused, one item per
	// machine. See host_outbox.go.
	AttentionOutbox = "outbox"
	// AttentionAsk is a question an agent or a script put to the person with
	// ask-human, with the answers it takes in Options. It closes when the
	// person answers or dismisses it, or the asking pane closes. See
	// ask_human.go.
	AttentionAsk = "ask"
	// AttentionPlan is a plan an agent in plan mode asks the person to
	// approve before it starts editing, held by the harness hook like an
	// approval. It shares the pane's blocking key with approval and question,
	// so it closes when the pane leaves needs_input, and the pane's
	// blocked_by stays approval for every consumer that reads it.
	AttentionPlan = "plan"
)

// AttentionKindNames lists the kinds in the order the Inbox groups them: what
// blocks an agent first, then what an agent said, then what went wrong, then
// what a restart left to bring back, then what finished, then mail still
// waiting to leave. list-attention sorts by it. A plan follows the approvals
// it is a larger kind of, and an ask sits with them: something is waiting on
// the answer.
var AttentionKindNames = []string{AttentionApproval, AttentionPlan, AttentionAsk, AttentionQuestion, AttentionMail, AttentionErrored, AttentionResume, AttentionFinished, AttentionOutbox}

// Close reasons an attention event carries on its closing action.
const (
	// AttentionClosedResolved: the fact behind the item stopped being true,
	// such as the pane leaving needs_input.
	AttentionClosedResolved = "resolved"
	// AttentionClosedSeen: an attached client focused the pane.
	AttentionClosedSeen = "seen"
	// AttentionClosedRead: the person's mail in the thread was read.
	AttentionClosedRead = "read"
	// AttentionClosedDismissed: a verified human dismissed it.
	AttentionClosedDismissed = "dismissed"
	// AttentionClosedWindow: the pane the item was about closed.
	AttentionClosedWindow = "window_closed"
	// AttentionClosedSession: the session the item was in ended.
	AttentionClosedSession = "session_closed"
	// AttentionClosedEvicted: the queue was over its cap and this was the oldest.
	AttentionClosedEvicted = "evicted"
	// AttentionClosedAnswered: the person answered a held approval from the
	// Inbox. The closing item carries the answer and who gave it.
	AttentionClosedAnswered = "answered"
	// AttentionClosedHostRemoved: the item came from a linked host that was
	// taken out of the [hosts] table.
	AttentionClosedHostRemoved = "host_removed"
	// AttentionClosedSnoozed: the person snoozed the item. It opens again
	// with the same id and since when the snooze ends or its fact changes.
	// A client that predates snoozing reads it as any other close.
	AttentionClosedSnoozed = "snoozed"
)

// Actions an attention event carries.
const (
	AttentionOpened  = "open"
	AttentionUpdated = "update"
	AttentionClosed  = "close"
)

// SortAttention puts items in Inbox order: by kind in AttentionKindNames
// order, then oldest first, then by id so the order is total.
func SortAttention(items []AttentionItem) {
	slices.SortFunc(items, func(x, y AttentionItem) int {
		if c := cmp.Compare(AttentionKindRank(x.Kind), AttentionKindRank(y.Kind)); c != 0 {
			return c
		}
		if c := cmp.Compare(x.Since, y.Since); c != 0 {
			return c
		}
		return cmp.Compare(x.Seq, y.Seq)
	})
}

// AttentionKindRank is a kind's place in the Inbox order. An unknown kind
// sorts last.
func AttentionKindRank(kind string) int {
	if i := slices.Index(AttentionKindNames, kind); i >= 0 {
		return i
	}
	return len(AttentionKindNames)
}

// Approval decisions. They are wire values: the decision reply-approval takes
// and request-approval returns.
const (
	// ApprovalOnce allows this one call.
	ApprovalOnce = "once"
	// ApprovalAlways allows it and tells the harness to stop asking for calls
	// like it, in whatever way the harness does that.
	ApprovalAlways = "always"
	// ApprovalDeny refuses the call.
	ApprovalDeny = "deny"
	// ApprovalAsk is not a decision: it ends the hold with none, so the
	// harness asks in its pane. It is what going to the pane means.
	ApprovalAsk = "ask"
)

// AttentionSelectorTarget is what a selector reads from an Inbox item on its
// own. The state is the one the kind stands for: an approval or a question is a
// pane on needs_input, errored is errored and finished is done. Mail and
// resume stand for no state. group and cwd are not on an item, and the caller
// fills them in when it knows the pane.
func AttentionSelectorTarget(it AttentionItem) SelectorTarget {
	t := SelectorTarget{
		Host:    it.Host,
		Session: it.Session,
		Name:    it.Name,
		Harness: it.Harness,
	}
	switch it.Kind {
	case AttentionApproval, AttentionQuestion, AttentionPlan:
		t.State, t.NeedsYou = AgentStateNeedsInput.Name(), true
	case AttentionErrored:
		t.State, t.NeedsYou = AgentStateErrored.Name(), true
	case AttentionFinished:
		t.State = AgentStateDone.Name()
	}
	return t
}

// ApprovalDetail is the get-approval result as a client decodes it. The
// daemon writes the same fields in verbGetApproval.
type ApprovalDetail struct {
	RequestID   string     `json:"request_id"`
	Kind        string     `json:"kind"`
	Session     string     `json:"session"`
	Window      string     `json:"window"`
	Summary     string     `json:"summary"`
	Tool        string     `json:"tool"`
	Target      string     `json:"target"`
	Options     []string   `json:"options"`
	AlwaysScope []string   `json:"always_scope"`
	Plan        string     `json:"plan"`
	PlanSHA     string     `json:"plan_sha"`
	Risk        []risk.Hit `json:"risk"`
	DenyMessage bool       `json:"deny_message"`
	Untrusted   bool       `json:"untrusted"`
}
