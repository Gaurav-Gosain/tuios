package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
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

// ErrVerbEditUnreviewed reports an allow for a held edit sent without
// edit_sha naming the digest of the body the hold serves. Nothing was
// answered and the hold runs on.
const ErrVerbEditUnreviewed = "edit_unreviewed"

// errEditUnreviewed is an allow of an edit without the digest of its body.
var errEditUnreviewed = errors.New("edit not reviewed")

// editUnreviewedError is the refusal for an allow of an edit whose body the
// reply does not name.
//
// It does not carry the digest: a refusal that named it would let a client
// allow the edit without ever fetching its body.
func editUnreviewedError() *verbError {
	return hintedVerbError(ErrVerbEditUnreviewed, "an allow of an edit must name in edit_sha the digest of the body that was shown", &VerbHint{
		Param:  "edit_sha",
		Verb:   "get-approval",
		Detail: "Nothing was answered and the hold runs on. Read the body with get-approval, show it, and send its edit_sha with the allow. A client that draws no diff answers the edit in the pane. A deny needs no edit_sha.",
	})
}

// fileToolCall reports whether a request is for a tool that writes a file,
// by the tool it names or by the tool its line names: the person reads the
// line, so a line that says Edit is an edit whatever tool rides beside it.
func fileToolCall(tool, summary string) bool {
	lineTool, _ := risk.ParseSummary(summary)
	for _, name := range []string{tool, lineTool} {
		if name != "" && slices.ContainsFunc(risk.FileTools, func(t string) bool { return strings.EqualFold(t, name) }) {
			return true
		}
	}
	return false
}

// checkEditRequest checks an edit request-approval holds: a body the Inbox can
// show as it is (integration.EditBody.Showable), on an approval, not a plan,
// and the same call the risk rules and the line read: tool and target are the
// body's tool and path, and summary is the line the hook reports for it.
func checkEditRequest(e *integration.EditBody, isPlan bool, tool, target, summary string) *verbError {
	switch {
	case isPlan:
		return invalidParam("edit", "edit is only taken with kind approval")
	case !e.Showable():
		return invalidParam("edit", "edit must be an Edit, MultiEdit or Write of at most "+strconv.Itoa(integration.MaxEdit)+" bytes of printable UTF-8 text, tabs and newlines")
	case tool != e.Tool:
		return invalidParam("tool", "tool must be the edit's tool")
	case target != e.Path:
		return invalidParam("target", "target must be the edit's path")
	case summary != "approve "+e.Tool+": "+e.Path:
		return invalidParam("summary", "summary must be the edit's line: approve, its tool, a colon and its path")
	}
	return nil
}

// getApprovalParams are what get-approval takes.
type getApprovalParams struct {
	RequestID string `json:"request_id"`
	Session   string `json:"session"`
}

// ApprovalDetail is the get-approval result as a client decodes it. The
// daemon writes the same fields in verbGetApproval.
type ApprovalDetail struct {
	RequestID   string   `json:"request_id"`
	Kind        string   `json:"kind"`
	Session     string   `json:"session"`
	Window      string   `json:"window"`
	Summary     string   `json:"summary"`
	Tool        string   `json:"tool"`
	Target      string   `json:"target"`
	Options     []string `json:"options"`
	AlwaysScope []string `json:"always_scope"`
	Plan        string   `json:"plan"`
	PlanSHA     string   `json:"plan_sha"`
	// Edit is a held edit's whole body, nil for any other hold. EditSHA is
	// the digest an allow of it must name.
	Edit        *integration.EditBody `json:"edit,omitempty"`
	EditSHA     string                `json:"edit_sha,omitempty"`
	Risk        []risk.Hit            `json:"risk"`
	DenyMessage bool                  `json:"deny_message"`
	Untrusted   bool                  `json:"untrusted"`
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
func (d *Daemon) verbGetApproval(cs *connState, params json.RawMessage) (any, *verbError) {
	var p getApprovalParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.RequestID == "" {
		return nil, invalidParam("request_id", "request_id is required: the request_id of the Inbox item")
	}
	h, scope, ok := d.attention.holdDetail(p.RequestID)
	if ok && p.Session != "" {
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		// A hold in another session is not found, so a caller held to its
		// own session learns nothing about the rest.
		ok = sess.Name() == h.session
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
	res := map[string]any{
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
		// The summary, the target, the plan and the edit are the agent's
		// text.
		"untrusted": true,
	}
	if h.edit != nil {
		res["edit"], res["edit_sha"] = h.edit, h.editSHA
	}
	return res, nil
}
