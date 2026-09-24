package session

import "encoding/json"

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

// verbGetApproval answers get-approval.
func (d *Daemon) verbGetApproval(_ *connState, params json.RawMessage) (any, *verbError) {
	var p getApprovalParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.RequestID == "" {
		return nil, invalidParam("request_id", "request_id is required: the request_id of the Inbox item")
	}
	return nil, notBuilt("get-approval")
}
