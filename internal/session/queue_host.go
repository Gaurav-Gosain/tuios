package session

import (
	"context"
	"errors"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Queueing a reply for an agent on another machine.
//
// queue-prompt with host goes to the daemon of that machine over this
// machine's link, on a connection of its own, as the reply's parts can be
// megabytes. The far daemon takes it as any call over the link: the entry is
// queued by link:THIS-MACHINE, its link policy here must allow write, a block
// or an image needs files too, and it writes the files and types the reply on
// its own machine, where the pane runs. See queue_parts.go.
//
// Only a caller outside every pane may send one: the person, with the nonce,
// or their own shell. A pane would have its grants here bypassed, since the
// far daemon sees only the link. A call that came over a link is not passed
// on again.

// queueHostBudget bounds a forward: the parts crossing the link, and the far
// daemon's answer.
const queueHostBudget = pasteHostedBudget

// queuePromptToHost forwards queue-prompt to the daemon on host.
func (d *Daemon) queuePromptToHost(cs *connState, p queuePromptParams) (any, *verbError) {
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		return nil, hintedVerbError(ErrVerbForbidden, "queue-prompt with host is for a caller on this machine", &VerbHint{
			Param:  "host",
			Detail: "It queues as this machine on another one. Nothing was queued.",
		})
	}
	if d.paneAuthority(cs) != nil {
		return nil, hintedVerbError(ErrVerbForbidden, "queue-prompt with host is refused from a pane", &VerbHint{
			Param:  "host",
			Detail: "Nothing was queued. The far machine would see only this machine's link, not the pane's grants. Use send-agent-message with host to leave the agent mail.",
		})
	}
	if p.Host == federation.LocalHostName {
		return nil, invalidParam("host", "host local is this machine: queue without host")
	}
	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}
	if p.Session == "" || p.Window == "" {
		return nil, invalidParam("session", "a reply for another machine names the session and the window there")
	}
	if p.HumanNonce != "" && !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, newVerbError(ErrVerbNotHuman, "human_nonce does not belong to a client attached right now. Nothing was queued")
	}
	if p.Parts != nil {
		if _, _, verr := checkQueueParts(p.Parts); verr != nil {
			return nil, verr
		}
	}
	params := map[string]any{"session": p.Session, "window": p.Window}
	if p.Text != "" {
		params["text"] = p.Text
	}
	if p.Parts != nil {
		params["parts"] = p.Parts
	}
	// The far daemon cannot verify this machine's nonce, so the entry is
	// the link's. The label says who sent it, as a claim.
	from := linkSelfName(d.hostedPaneHostName())
	if p.HumanNonce != "" {
		from = "the person on " + firstNonEmpty(from, "the hub")
	}
	if from != "" {
		params["from"] = from
	}
	ctx, cancel := context.WithTimeout(d.ctx, queueHostBudget)
	defer cancel()
	var out map[string]any
	err := (fileEnd{d: d, host: p.Host}).callTimeout(ctx, "queue-prompt", params, &out, queueHostBudget)
	if far, ok := errors.AsType[*VerbCallError](err); ok && far.Code == ErrVerbForbidden && p.Parts != nil && p.Text != "" &&
		!hasImageParts(p.Parts) && strings.Contains(far.Message, "does not allow files") {
		// The far machine's policy for this link has no files, so a block
		// cannot go as a file there. The message as one text fits the limit,
		// so it goes that way, each block typed in its place.
		delete(params, "parts")
		out = nil
		err = (fileEnd{d: d, host: p.Host}).callTimeout(ctx, "queue-prompt", params, &out, queueHostBudget)
	}
	if err != nil {
		if far, ok := errors.AsType[*VerbCallError](err); ok {
			return nil, hintedVerbError(far.Code, "queue-prompt on "+p.Host+": "+far.Message, refusedOn(far.Hint, p.Host))
		}
		return nil, hintedVerbError(ErrVerbHostUnreachable, "queue-prompt: the reply did not reach "+p.Host+": "+err.Error(), &VerbHint{
			Detail: "Nothing was queued. Check that the link to " + p.Host + " is up and that its tuios is current.",
		})
	}
	if out == nil {
		out = map[string]any{}
	}
	out["host"] = p.Host
	return out, nil
}

// queueHostRefusal is why a queue verb with host is refused before it goes
// anywhere: one that came over a link, one from a pane, one that names this
// machine, and one with no pane there. nil lets it through.
func (d *Daemon) queueHostRefusal(cs *connState, verb, host, session string) *verbError {
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		return hintedVerbError(ErrVerbForbidden, verb+" with host is for a caller on this machine", &VerbHint{Param: "host"})
	}
	if d.paneAuthority(cs) != nil {
		return hintedVerbError(ErrVerbForbidden, verb+" with host is refused from a pane", &VerbHint{
			Param:  "host",
			Detail: "The far machine would see only this machine's link, not the pane's grants.",
		})
	}
	if host == federation.LocalHostName {
		return invalidParam("host", "host local is this machine: call "+verb+" without host")
	}
	if verr := d.checkHostParam(host); verr != nil {
		return verr
	}
	if session == "" {
		return invalidParam("session", verb+" for another machine names the session there")
	}
	return nil
}

// listQueuedOnHost forwards list-queued to the daemon on host, which lists
// the pane's queue there as it would for any caller over the link. The
// person's replies queued there are the link's, by link:THIS-MACHINE, with
// "the person on THIS-MACHINE" as their from.
func (d *Daemon) listQueuedOnHost(cs *connState, p listQueuedParams) (any, *verbError) {
	if verr := d.queueHostRefusal(cs, "list-queued", p.Host, p.Session); verr != nil {
		return nil, verr
	}
	params := map[string]any{"session": p.Session}
	if p.Window != "" {
		params["window"] = p.Window
	}
	ctx, cancel := context.WithTimeout(d.ctx, queueHostBudget)
	defer cancel()
	var out map[string]any
	if err := (fileEnd{d: d, host: p.Host}).callTimeout(ctx, "list-queued", params, &out, queueHostBudget); err != nil {
		return nil, queueHostError("list-queued", p.Host, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	out["host"] = p.Host
	return out, nil
}

// cancelQueuedOnHost forwards cancel-queued to the daemon on host. The far
// daemon lets the link drop only the entries it queued, which are the ones
// this machine sent there. The person's nonce is checked here, as the far
// daemon cannot check it.
func (d *Daemon) cancelQueuedOnHost(cs *connState, p cancelQueuedParams) (any, *verbError) {
	if verr := d.queueHostRefusal(cs, "cancel-queued", p.Host, p.Session); verr != nil {
		return nil, verr
	}
	if p.HumanNonce != "" && !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, newVerbError(ErrVerbNotHuman, "human_nonce does not belong to a client attached right now. Nothing was dropped")
	}
	params := map[string]any{"session": p.Session}
	for k, v := range map[string]string{"window": p.Window, "id": p.ID} {
		if v != "" {
			params[k] = v
		}
	}
	if p.All {
		params["all"] = true
	}
	ctx, cancel := context.WithTimeout(d.ctx, queueHostBudget)
	defer cancel()
	var out map[string]any
	if err := (fileEnd{d: d, host: p.Host}).callTimeout(ctx, "cancel-queued", params, &out, queueHostBudget); err != nil {
		return nil, queueHostError("cancel-queued", p.Host, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	out["host"] = p.Host
	return out, nil
}

// queueHostError is a forwarded queue verb's failure, named for its machine.
func queueHostError(verb, host string, err error) *verbError {
	if far, ok := errors.AsType[*VerbCallError](err); ok {
		return hintedVerbError(far.Code, verb+" on "+host+": "+far.Message, refusedOn(far.Hint, host))
	}
	return hintedVerbError(ErrVerbHostUnreachable, verb+": did not reach "+host+": "+err.Error(), &VerbHint{
		Detail: "Check that the link to " + host + " is up and that its tuios is current.",
	})
}
