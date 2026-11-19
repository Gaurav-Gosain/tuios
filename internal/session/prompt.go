package session

import (
	"encoding/json"
	"slices"
	"sync"
	"time"
)

// Prompts: a text field or a list in the palette of the attached clients.
//
// An action, a script or a plugin that needs a value from the person calls
// prompt. The daemon pushes the prompt to every client attached on this
// machine that shows prompts (MsgPrompt), and the client opens it in its
// command palette: a field to type in, or a list to pick from. The first
// answer from any client wins, and the push that closes it takes it off
// every other client.
//
// It borrows the proof ask-human uses (ask_human.go). Only answer-prompt
// puts an answer in, with the nonce of a client attached right now, from a
// process outside every pane (human_sender.go). So an agent, a plugin or a
// script cannot answer a prompt, its own included. Unlike ask-human it does
// not ride the Inbox: the Inbox is an agent feature, and a prompt works with
// [agents] enabled = false. A prompt is never kept: it ends with the call
// that waits on it.
//
// A call that gets no answer says why, so a script can tell the cases apart:
// cancelled (Esc at a client), timeout, no_client (no attached client could
// show it, so nothing was shown; an older client does not say it shows
// prompts and does not count), busy (another prompt is open: one shows at a
// time, since every client shows it in the same place) and shutdown.
//
// A prompt is refused over a link: it asks the person at this machine.

// Prompt kinds.
const (
	PromptInput = "input"
	PromptPick  = "pick"
)

// Prompt statuses. They are wire values: the status prompt returns.
const (
	PromptAnswered  = "answered"
	PromptCancelled = "cancelled"
	PromptTimeout   = "timeout"
	PromptNoClient  = "no_client"
	PromptBusy      = "busy"
	promptShutdown  = "shutdown"
)

// Bounds on a prompt. promptMaxValue bounds what the person types.
const (
	promptMaxItems = 1000
	promptMaxID    = 128
	promptMaxIcon  = 16
	promptMaxValue = 4096
)

// defaultPromptWait is how long prompt waits when the caller names no
// timeout. The most it waits is maxAskWait.
var defaultPromptWait = 120 * time.Second

// PromptItem is one row of a pick. ID is what the answer names, unique in
// the prompt. Title is the row's text, which the filter matches. Subtitle is
// a quiet note on the right, and Icon a short glyph before the title.
type PromptItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Icon     string `json:"icon,omitempty"`
}

// PromptRequest is one open prompt, as the clients show it. It is also the
// params of prompt, which sets ID and From itself.
type PromptRequest struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	Placeholder string       `json:"placeholder,omitempty"`
	Default     string       `json:"default,omitempty"`
	Items       []PromptItem `json:"items,omitempty"`
	Multiple    bool         `json:"multiple,omitempty"`
	AllowOther  bool         `json:"allow_other,omitempty"`
	// From says who asks: a pane, a plugin or a command. The palette shows
	// it, so the person knows what the answer goes to.
	From string `json:"from,omitempty"`
}

// PromptPayload is the body of MsgPrompt. Prompt is the open prompt. When
// it is nil, Closed names the prompt that ended and Reason how it ended.
type PromptPayload struct {
	Prompt *PromptRequest
	Closed string
	Reason string
}

// PromptResult is prompt's answer, and how a prompt ended.
type PromptResult struct {
	Type       string   `json:"type"`
	Status     string   `json:"status"`
	RequestID  string   `json:"request_id,omitempty"`
	Value      string   `json:"value,omitempty"`
	IDs        []string `json:"ids,omitempty"`
	Other      string   `json:"other,omitempty"`
	AnsweredBy string   `json:"answered_by,omitempty"`
}

// promptStore holds the one open prompt and the channel its call waits on.
type promptStore struct {
	mu   sync.Mutex
	open *PromptRequest
	done chan PromptResult
	// pushMu orders the prompt pushes. The open and the close of a prompt,
	// and the open an attaching client is sent, each take their place in a
	// client's broadcast order under it. See pushPrompt.
	pushMu sync.Mutex
}

// start opens req, or reports false when a prompt is open.
func (s *promptStore) start(req *PromptRequest) (chan PromptResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open != nil {
		return nil, false
	}
	s.open, s.done = req, make(chan PromptResult, 1)
	return s.done, true
}

// finish ends the open prompt id with out, when it is still open, and
// reports whether it was. checked runs first, under the lock.
func (s *promptStore) finish(id string, out PromptResult, check func(*PromptRequest) *verbError) (bool, *verbError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil || s.open.ID != id {
		return false, nil
	}
	if check != nil {
		if verr := check(s.open); verr != nil {
			return false, verr
		}
	}
	s.open = nil
	s.done <- out
	return true, nil
}

// current is the open prompt, or nil.
func (s *promptStore) current() *PromptRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// checkPromptAnswer checks an answer against what the prompt offered.
func checkPromptAnswer(req *PromptRequest, out PromptResult) *verbError {
	n := len(out.IDs)
	if out.Other != "" {
		n++
	}
	bad := ""
	switch {
	case out.Status == PromptCancelled:
		if out.Value != "" || n > 0 {
			bad = "a cancel carries no value, ids or other"
		}
	case req.Kind == PromptInput:
		if n > 0 || !promptValueOK(out.Value) {
			bad = "an input takes value: one line of printable text, at most 4096 bytes"
		}
	case out.Value != "" || n == 0 || n > 1 && !req.Multiple || out.Other != "" && (!req.AllowOther || !promptValueOK(out.Other)):
		bad = "a pick takes ids of its items, one unless it takes multiple, and other only when it allows other"
	}
	for i, id := range out.IDs {
		if !slices.ContainsFunc(req.Items, func(it PromptItem) bool { return it.ID == id }) || slices.Index(out.IDs, id) != i {
			bad = "ids name items of the prompt, each once"
		}
	}
	if bad != "" {
		return invalidParam("ids", bad)
	}
	return nil
}

// promptValueOK reports whether s is text the person may type: one line of
// printable text, at most promptMaxValue bytes. Empty is allowed.
func promptValueOK(s string) bool {
	return len(s) <= promptMaxValue && askLinePrintable(s)
}

// promptLineOK reports whether s is a line the palette shows as written,
// at most limit bytes. Empty is allowed.
func promptLineOK(s string, limit int) bool {
	return s == "" || askLineShown(s, limit)
}

// checkPromptRequest checks what a prompt asks before anything is shown.
func checkPromptRequest(p *PromptRequest) *verbError {
	pick := p.Kind == PromptPick
	switch {
	case !pick && p.Kind != PromptInput:
		return invalidParam("kind", "kind is input (a text field) or pick (a list)", PromptInput, PromptPick)
	case p.Title == "" || !askLineShown(p.Title, attentionMaxSummary) || !promptLineOK(p.Placeholder, attentionMaxSummary):
		return invalidParam("title", "title is required, and title and placeholder are one line of printable text, at most 160 bytes")
	case !pick && (len(p.Items) > 0 || p.Multiple || p.AllowOther || !promptValueOK(p.Default)):
		return invalidParam("items", "an input takes no items, multiple or allow_other, and its default is one line")
	case pick && (p.Default != "" || len(p.Items) == 0 || len(p.Items) > promptMaxItems):
		return invalidParam("items", "a pick takes 1 to 1000 items and no default")
	}
	for i, it := range p.Items {
		if it.ID == "" || len(it.ID) > promptMaxID || !askLinePrintable(it.ID) ||
			slices.IndexFunc(p.Items, func(o PromptItem) bool { return o.ID == it.ID }) != i ||
			it.Title == "" || !askLineShown(it.Title, attentionMaxSummary) ||
			!promptLineOK(it.Subtitle, attentionMaxSummary) || !promptLineOK(it.Icon, promptMaxIcon) {
			return invalidParam("items", "item "+echoName(it.ID)+": each item needs a unique id and a title, and every field is one line of printable text")
		}
	}
	return nil
}

// promptReader reports whether cs is an attached client of this machine
// that shows prompts and takes keys from the person at it. The upgrade
// dialog's mark says the same of a client that takes keys: the tuios TUI,
// and not gui-bridge. See personClients.
func promptReader(cs *connState) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.isTUIClient && cs.attached && cs.prompts && cs.upgradeDialog && !cs.viaLink && !cs.passive && !cs.viewOnly
}

// pushPrompt sends p to every client that shows prompts, and reports how
// many of them could answer. A nil p sends nothing.
//
// Each client is sent one open and one close of a prompt, in that order. An
// attaching client is admitted to the pushes before sendPrompt sends it the
// open prompt, so the open can reach it both ways: the open goes only to a
// client whose promptShown is another prompt, and the close only to one whose
// promptShown is this prompt. Under pushMu, and on the broadcast order, a
// close cannot pass the open it closes.
func (d *Daemon) pushPrompt(p *PromptPayload) int {
	d.prompts.pushMu.Lock()
	defer d.prompts.pushMu.Unlock()
	var readers []*connState
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		if promptReader(cs) {
			readers = append(readers, cs)
		}
	}
	d.clientsMu.RUnlock()
	var msg *Message
	if p != nil {
		msg, _ = NewMessage(MsgPrompt, p)
	}
	n := 0
	for _, cs := range readers {
		if msg != nil && cs.markPromptShown(p) {
			d.queueBroadcast(cs, msg, "pushPrompt")
		}
		if d.mayActAsHuman(cs) {
			n++
		}
	}
	return n
}

// sendPrompt sends a client that just attached the open prompt, unless the
// push of it reached the client already. See pushPrompt.
func (d *Daemon) sendPrompt(cs *connState) {
	d.prompts.pushMu.Lock()
	defer d.prompts.pushMu.Unlock()
	req := d.prompts.current()
	if req == nil || !promptReader(cs) {
		return
	}
	p := &PromptPayload{Prompt: req}
	if !cs.markPromptShown(p) {
		return
	}
	if msg, err := NewMessage(MsgPrompt, p); err == nil {
		d.queueBroadcast(cs, msg, "sendPrompt")
	}
}

// markPromptShown records that p goes to cs, and reports whether it should:
// an open that cs was not sent yet, or the close of the prompt cs shows.
func (cs *connState) markPromptShown(p *PromptPayload) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	switch {
	case p.Prompt != nil && cs.promptShown != p.Prompt.ID:
		cs.promptShown = p.Prompt.ID
		return true
	case p.Prompt == nil && cs.promptShown != "" && cs.promptShown == p.Closed:
		cs.promptShown = ""
		return true
	}
	return false
}

// promptFrom says who asks, for the palette: the pane, the plugin, or a
// command outside every pane.
func (d *Daemon) promptFrom(cs *connState) string {
	if pa := d.pluginAuthority(cs); pa != nil {
		return "plugin " + pa.plugin
	}
	if fromPane, own := d.peerPane(cs); fromPane {
		return "pane " + shortID(own)
	}
	return "a command"
}

// verbPrompt shows a field or a list in the palette of the attached
// clients, and waits for the answer. See the file comment.
func (d *Daemon) verbPrompt(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		PromptRequest
		Timeout int `json:"timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs != nil && cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "prompt is refused over a link: it asks the person at this machine")
	}
	if verr := checkPromptRequest(&p.PromptRequest); verr != nil {
		return nil, verr
	}
	if p.Timeout < 0 {
		return nil, invalidParam("timeout", "timeout is milliseconds and cannot be negative")
	}
	timeout := defaultPromptWait
	if p.Timeout > 0 {
		timeout = min(time.Duration(p.Timeout)*time.Millisecond, maxAskWait)
	}
	res := PromptResult{Type: "prompt_result", Status: PromptNoClient}
	if d.pushPrompt(nil) == 0 {
		return res, nil
	}
	req := &p.PromptRequest
	req.ID, req.From = newApprovalID(), d.promptFrom(cs)
	done, ok := d.prompts.start(req)
	if !ok {
		res.Status = PromptBusy
		return res, nil
	}
	d.pushPrompt(&PromptPayload{Prompt: req})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ended := PromptTimeout
	select {
	case res = <-done:
		ended = ""
	case <-timer.C:
	case <-d.ctx.Done():
		ended = promptShutdown
	}
	if ended != "" {
		// An answer that raced the end is in done already; otherwise the
		// end goes there.
		_, _ = d.prompts.finish(req.ID, PromptResult{Status: ended}, nil)
		res = <-done
	}
	d.pushPrompt(&PromptPayload{Closed: req.ID, Reason: res.Status})
	res.Type, res.RequestID = "prompt_result", req.ID
	return res, nil
}

// verbAnswerPrompt answers the open prompt for the person.
func (d *Daemon) verbAnswerPrompt(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		PromptResult
		HumanNonce string `json:"human_nonce"`
		Cancel     bool   `json:"cancel"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	h, ok := d.humanNonceHolder(p.HumanNonce, "", cs)
	if !ok || cs != nil && cs.viaLink {
		return nil, newVerbError(ErrVerbNotHuman, "answer-prompt is for the person at a client attached on this machine. Nothing was answered")
	}
	out := p.PromptResult
	out.Status, out.AnsweredBy = PromptAnswered, h.clientID
	if p.Cancel {
		out.Status = PromptCancelled
	}
	applied, verr := d.prompts.finish(p.RequestID, out, func(req *PromptRequest) *verbError { return checkPromptAnswer(req, out) })
	if verr != nil {
		return nil, verr
	}
	if !applied {
		return nil, invalidParam("request_id", "no prompt is open under request "+echoName(p.RequestID))
	}
	return map[string]any{"type": "prompt_answered", "request_id": p.RequestID, "applied": true, "status": out.Status}, nil
}
