package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The delivery queue: messages for an agent, typed as a prompt when the agent
// comes to rest.
//
// queue-prompt types into a pane, later, so it is scopeWrite and a typing verb
// (typingVerbs): at queue time the target must hold nothing the caller does
// not, and must not be on needs_input unless the caller holds respond. The
// delivery repeats those checks against the caller's grants as they are then,
// and refuses to type over a prompt whatever the entry. cancel-queued writes
// the queue, so it is scopeWrite too, and a pane drops only what it queued.
// list-queued reads. Over a link, queue-prompt and cancel-queued need write
// and list-queued needs list.

// ErrVerbQueueFull reports a pane whose delivery queue holds as many entries
// as [agents.queue] max allows. Nothing was queued.
const ErrVerbQueueFull = "queue_full"

// queueMaxText bounds one queued message, in bytes.
const queueMaxText = 16 << 10

// queuePromptParams are what queue-prompt takes.
type queuePromptParams struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	Text       string `json:"text"`
	HumanNonce string `json:"human_nonce"`
	From       string `json:"from"`
}

// verbQueuePrompt answers queue-prompt.
func (d *Daemon) verbQueuePrompt(_ *connState, params json.RawMessage) (any, *verbError) {
	var p queuePromptParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, invalidParam("text", "text is required: the message to type when the agent is at rest")
	}
	if len(p.Text) > queueMaxText {
		return nil, invalidParam("text", "text is longer than "+strconv.Itoa(queueMaxText)+" bytes")
	}
	return nil, notBuilt("queue-prompt")
}

// listQueuedParams are what list-queued takes.
type listQueuedParams struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}

// verbListQueued answers list-queued.
func (d *Daemon) verbListQueued(_ *connState, params json.RawMessage) (any, *verbError) {
	var p listQueuedParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	return nil, notBuilt("list-queued")
}

// cancelQueuedParams are what cancel-queued takes.
type cancelQueuedParams struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	ID         string `json:"id"`
	All        bool   `json:"all"`
	HumanNonce string `json:"human_nonce"`
}

// verbCancelQueued answers cancel-queued.
func (d *Daemon) verbCancelQueued(_ *connState, params json.RawMessage) (any, *verbError) {
	var p cancelQueuedParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.ID == "" && !p.All {
		return nil, invalidParam("id", "name the entry to drop with id, or pass all")
	}
	return nil, notBuilt("cancel-queued")
}
