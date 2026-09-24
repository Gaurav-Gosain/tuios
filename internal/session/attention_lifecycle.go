package session

import (
	"encoding/json"
	"slices"
)

// The Inbox's lifecycle beyond open and close: snoozing an item, waking it,
// marking a finished pane unread, and restoring an item the person closed a
// moment ago.
//
// Every one of them is the person's act on the list the person reads, so
// mark-attention takes the proof dismiss-attention and reply-approval take: a
// live nonce from an attached client, over the kind of connection it was
// issued on, and not from inside a pane (verifyAnyHumanNonce). It is scopeDeny
// for a restricted connection and for a pane without admin, and a link needs
// respond. An agent therefore cannot snooze, un-read or restore the Inbox.

// markAttentionParams are what mark-attention takes.
type markAttentionParams struct {
	ID          string `json:"id"`
	Session     string `json:"session"`
	Window      string `json:"window"`
	Kind        string `json:"kind"`
	Action      string `json:"action"`
	Until       int64  `json:"until"`
	ForMS       int64  `json:"for_ms"`
	UntilChange bool   `json:"until_change"`
	HumanNonce  string `json:"human_nonce"`
}

// verbMarkAttention answers mark-attention. The person's proof is checked
// before anything else, so the verb refuses an agent from the start.
func (d *Daemon) verbMarkAttention(cs *connState, params json.RawMessage) (any, *verbError) {
	var p markAttentionParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(markAttentionActions, p.Action) {
		return nil, invalidParam("action", "action is one of snooze, wake, unread and restore", markAttentionActions...)
	}
	if p.ID == "" && (p.Window == "" || p.Kind == "") {
		return nil, invalidParam("id", "name the item with id, or with session, window and kind")
	}
	if !d.verifyAnyHumanNonce(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "mark-attention is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Only a client attached right now can snooze, wake, mark unread or restore an Inbox item, by passing the nonce its attach reply carried. An agent cannot change what the person reads.",
		})
	}
	return nil, notBuilt("mark-attention")
}
