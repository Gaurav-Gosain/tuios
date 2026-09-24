package session

import (
	"encoding/json"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// Reading a held approval whole, and the answers that need more than the
// summary line: risk acknowledgements and plans.
//
// get-approval reads, so it is scopeRead and a link needs list. It names a hold
// by request id rather than by session, so it also takes session: from a pane
// without admin the session is filled in as the pane's own, and a hold in
// another session is not found. reply-approval and respond keep their own
// rules: the person's nonce, or the respond grant for respond.

// ErrVerbRiskUnacknowledged reports an allow for an approval that matched a
// risk rule, sent without risk_ack naming exactly the rules it matched.
// Nothing was answered.
const ErrVerbRiskUnacknowledged = "risk_unacknowledged"

// getApprovalParams are what get-approval takes.
type getApprovalParams struct {
	RequestID string `json:"request_id"`
	Session   string `json:"session"`
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

// holdDetail copies a running hold and the scope its item shows.
func (a *attentionStore) holdDetail(requestID string) (approvalHold, []string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.holds[requestID]
	if !ok {
		return approvalHold{}, nil, false
	}
	var scope []string
	if it, ok := a.items[h.itemID]; ok {
		scope = slices.Clone(it.AlwaysScope)
	}
	return *h, scope, true
}

// verbGetApproval answers get-approval: a running hold, whole.
func (d *Daemon) verbGetApproval(_ *connState, params json.RawMessage) (any, *verbError) {
	var p getApprovalParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.RequestID == "" {
		return nil, invalidParam("request_id", "request_id is required: the request_id of the Inbox item")
	}
	h, scope, ok := d.attention.holdDetail(p.RequestID)
	if ok && p.Session != "" {
		sess, verr := d.resolveVerbSession(p.Session)
		if verr != nil {
			return nil, verr
		}
		// A hold in another session is not found, so a caller held to its
		// own session learns nothing about the rest.
		ok = sess.Name == h.session
	}
	if !ok {
		return nil, noHoldError("no approval is held under request " + echoName(p.RequestID))
	}
	hits := h.risk
	if hits == nil {
		hits = []risk.Hit{}
	}
	options := h.options
	if options == nil {
		options = []string{}
	}
	if scope == nil {
		scope = []string{}
	}
	return map[string]any{
		"type":         "approval",
		"request_id":   h.id,
		"kind":         h.kind,
		"session":      h.session,
		"window":       h.window,
		"summary":      h.summary,
		"tool":         h.tool,
		"target":       h.target,
		"options":      options,
		"always_scope": scope,
		"plan":         h.plan,
		"plan_sha":     h.planSHA,
		"risk":         hits,
		"deny_message": h.denyMessage,
		// The summary, the target and the plan are the agent's text.
		"untrusted": true,
	}, nil
}
