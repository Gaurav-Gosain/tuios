package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
)

// Asking the person a question, and getting the answer back.
//
// An agent that needs a decision had two poor options: stop and wait in its own
// pane, where nobody may be looking, or mail human and poll its inbox. ask-human
// is one call for both cases. It puts the question in the Inbox as an ask item
// with the answers it takes, and blocks until the person picks one or the wait
// runs out:
//
//   - When a client the person holds shows the asking pane, that client opens
//     the Inbox on the question at once, which is the popup: a key picks the
//     answer. Anywhere else the item waits in the Inbox with the usual alert,
//     so a question never steals the keyboard from someone typing elsewhere.
//   - When nobody is attached, the item waits in the Inbox for the next
//     attach. The Inbox is the one place to look either way.
//   - When the wait runs out, the call returns status pending and the question
//     stays. The answer, when it comes, is mailed to the asking pane from
//     human, marked verified_human, so wait-for agent-message picks it up. A
//     caller with no pane calls ask-human again with the request id to wait
//     for it or read it.
//
// Who may do what, and how it is enforced:
//
//   - Only answer-ask puts an answer in, and it takes the proof reply-approval
//     takes: the nonce of a client attached right now, from a process outside
//     every pane (human_sender.go, human_origin.go). An agent cannot answer
//     its own question or another's. The answer must be one of the options
//     the question offered, and when the client names the line it showed, the
//     line must be the question's, so an answer only ever answers what the
//     person read.
//   - A caller inside a pane asks only as its own pane: naming another is
//     forbidden, so no agent can have an answer mailed into another agent's
//     inbox. It may come back for an answer only to a question its own pane
//     asked. A caller outside every pane is the user's own script and may name
//     any pane, or none.
//   - ask-human is refused over a link. A question is put to the person at
//     this machine's clients by something on this machine.
//   - The question and each option must be one line of printable text that
//     the Inbox shows exactly as written, the check a held approval's line
//     passes, so nothing is cut, masked or rewritten between the asker and
//     the person. Questions from outside every pane are capped per session,
//     and a pane has one open question at a time: a new one supersedes it.
//
// What an ask grants the asker is the answer to its own question, which is no
// more than a message to human and the person's reply already gives it.

// Bounds on a question.
const (
	askMaxOptions = 9
	askMaxOption  = 60
	// askMaxOutside bounds the open questions with no pane, per session.
	askMaxOutside = 16
	// askSettledMax bounds how many ended asks are remembered for a caller
	// that comes back for the answer.
	askSettledMax = 256
)

// defaultAskWait is how long ask-human blocks when the caller names no
// timeout, and maxAskWait the most it will. The question outlives either.
var (
	defaultAskWait = 120 * time.Second
	maxAskWait     = time.Hour
)

// Ask statuses. They are wire values: the status ask-human returns. The
// attention close reasons (dismissed, window_closed, session_closed, evicted)
// are reported as they are.
const (
	AskAnswered = AttentionClosedAnswered
	AskPending  = "pending"
	// AttentionClosedSuperseded closes an ask its pane replaced with a newer
	// one.
	AttentionClosedSuperseded = "superseded"
	askEndShutdown            = "shutdown"
)

// askOutcome is how an ask ended.
type askOutcome struct {
	Answer string
	Index  int
	By     string
	Reason string
	// window is the asking pane, kept so a caller coming back for a settled
	// answer can be held to its own pane's questions.
	window string
}

// askHold is one open question.
type askHold struct {
	id       string
	itemID   string
	session  string
	window   string
	question string
	options  []string
	// waiting is true while an ask-human call blocks on this question. An
	// answer then goes down done; with nobody waiting it is mailed.
	waiting bool
	done    chan askOutcome
}

var (
	errAskBusy    = errors.New("another call is waiting on this question")
	errAskTooMany = errors.New("too many open questions")
)

// openAsk puts a question in the Inbox. it carries the item's session, pane,
// name, harness, workspace, question (Summary) and answers (Options). A pane
// with an open question has it superseded.
func (a *attentionStore) openAsk(it AttentionItem, waiting bool) (*askHold, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	outside := 0
	for _, id := range a.sortedIDsLocked() {
		cur := a.items[id]
		if cur.Kind != AttentionAsk || cur.Session != it.Session {
			continue
		}
		switch {
		case it.Window != "" && cur.Window == it.Window:
			a.closeLocked(id, AttentionClosedSuperseded)
		case cur.Window == "":
			outside++
		}
	}
	if it.Window == "" && outside >= askMaxOutside {
		return nil, errAskTooMany
	}
	if len(a.items) >= attentionMaxItems {
		a.evictOldestLocked()
	}
	if a.asks == nil {
		a.asks = make(map[string]*askHold)
	}
	h := &askHold{
		id:       newApprovalID(),
		session:  it.Session,
		window:   it.Window,
		question: it.Summary,
		options:  slices.Clone(it.Options),
		waiting:  waiting,
		done:     make(chan askOutcome, 1),
	}
	a.nextID++
	a.rev++
	it.Kind = AttentionAsk
	it.ID = strconv.FormatUint(a.nextID, 10)
	it.RequestID = h.id
	it.Seq = a.rev
	it.Since = time.Now().UnixNano()
	h.itemID = it.ID
	a.asks[h.id] = h
	a.items[it.ID] = &it
	a.byKey[attentionItemKey(&it)] = it.ID
	a.publish(attentionEvent(AttentionOpened, it))
	a.changedLocked()
	return h, nil
}

// endAskLocked ends an open question with out, handing it to a waiting call.
// The caller holds mu and closes or has closed the item.
func (a *attentionStore) endAskLocked(requestID string, out askOutcome) {
	h, ok := a.asks[requestID]
	if !ok {
		return
	}
	delete(a.asks, requestID)
	out.window = h.window
	if a.askSettled == nil {
		a.askSettled = make(map[string]askOutcome)
	}
	if _, seen := a.askSettled[requestID]; !seen {
		a.askSettledOrder = append(a.askSettledOrder, requestID)
	}
	a.askSettled[requestID] = out
	for len(a.askSettledOrder) > askSettledMax {
		delete(a.askSettled, a.askSettledOrder[0])
		a.askSettledOrder = a.askSettledOrder[1:]
	}
	if h.waiting {
		h.waiting = false
		h.done <- out
	}
}

// askPane is the pane that asked a question, open or settled, and whether
// the request is known at all.
func (a *attentionStore) askPane(requestID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h, ok := a.asks[requestID]; ok {
		return h.window, true
	}
	if out, ok := a.askSettled[requestID]; ok {
		return out.window, true
	}
	return "", false
}

// joinAsk is a caller coming back for a question: it waits on it when it is
// still open, or gets how it ended.
func (a *attentionStore) joinAsk(requestID string) (*askHold, askOutcome, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h, ok := a.asks[requestID]; ok {
		if h.waiting {
			return nil, askOutcome{}, false, errAskBusy
		}
		h.waiting = true
		return h, askOutcome{}, false, nil
	}
	if out, ok := a.askSettled[requestID]; ok {
		return nil, out, true, nil
	}
	return nil, askOutcome{}, false, errNoHold
}

// leaveAsk is a waiting call giving up. It reports the outcome when the
// question ended while the call was on its way out, so an answer that raced
// the timeout is returned rather than lost.
func (a *attentionStore) leaveAsk(h *askHold) (askOutcome, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, open := a.asks[h.id]; open {
		h.waiting = false
		return askOutcome{}, false
	}
	select {
	case out := <-h.done:
		return out, true
	default:
		return askOutcome{}, false
	}
}

// answerAsk puts the person's answer in. The first answer wins: a later one
// gets how the question ended, with applied false. mail reports that nobody
// was waiting and the question came from a pane, so the answer is to be
// mailed there.
func (a *attentionStore) answerAsk(requestID, answer, by, shown string) (out askOutcome, h askHold, applied, mail bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hold, ok := a.asks[requestID]
	if !ok {
		if prev, ok := a.askSettled[requestID]; ok {
			return prev, askHold{id: requestID}, false, false, nil
		}
		return askOutcome{}, askHold{}, false, false, errNoHold
	}
	idx := slices.Index(hold.options, answer)
	if idx < 0 {
		return askOutcome{}, *hold, false, false, errBadDecision
	}
	if shown != "" && shown != hold.question {
		return askOutcome{Reason: approvalEndChanged}, *hold, false, false, nil
	}
	out = askOutcome{Answer: answer, Index: idx + 1, By: by, Reason: AskAnswered}
	mail = !hold.waiting && hold.window != ""
	h = *hold
	a.endAskLocked(requestID, out)
	a.closeWithLocked(hold.itemID, AttentionClosedAnswered, func(it *AttentionItem) {
		it.Answer, it.AnsweredBy = answer, by
	})
	return out, h, true, mail, nil
}

// askLineShown reports whether s is one line the Inbox shows exactly as
// written, at most limit bytes.
func askLineShown(s string, limit int) bool {
	return len(s) <= limit && approvalLineShown(s)
}

// askResult is ask-human's answer.
func askResult(requestID string, out askOutcome) map[string]any {
	status := out.Reason
	if status == "" {
		status = AskPending
	}
	res := map[string]any{
		"type":       "human_answer",
		"request_id": requestID,
		"status":     status,
		// Set only when the person answered through a client whose attach
		// the daemon checked, which is the only way an ask is answered.
		"verified_human": status == AskAnswered,
	}
	if status == AskAnswered {
		res["answer"] = out.Answer
		res["answer_index"] = out.Index
		res["answered_by"] = out.By
	}
	return res
}

// verbAskHuman puts a question to the person and waits for the answer. See
// the file comment for who may ask and how it ends.
func (d *Daemon) verbAskHuman(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string   `json:"session"`
		Window    string   `json:"window"`
		Question  string   `json:"question"`
		Options   []string `json:"options"`
		Timeout   int      `json:"timeout"`
		Wait      *bool    `json:"wait"`
		RequestID string   `json:"request_id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs != nil && cs.viaLink {
		return nil, hintedVerbError(ErrVerbForbidden, "ask-human is refused over a link: a question is put to the person at this machine's clients by something on this machine", &VerbHint{
			Detail: "Nothing was asked. Ask on the machine the person is attached to.",
		})
	}
	if p.Timeout < 0 {
		return nil, invalidParam("timeout", "timeout is milliseconds and cannot be negative")
	}
	wait := p.Wait == nil || *p.Wait
	timeout := defaultAskWait
	if p.Timeout > 0 {
		timeout = min(time.Duration(p.Timeout)*time.Millisecond, maxAskWait)
	}
	fromPane, own := d.peerPane(cs)

	if p.RequestID != "" {
		if p.Question != "" || len(p.Options) > 0 {
			return nil, invalidParam("request_id", "request_id comes back for a question already asked, so it takes no question or options")
		}
		return d.rejoinAsk(p.RequestID, fromPane, own, wait, timeout)
	}

	if !askLineShown(p.Question, attentionMaxSummary) {
		return nil, invalidParam("question", "question is required: one line of printable text, at most 160 bytes, that the Inbox shows as written. Put the details in a message to human and ask the short question here")
	}
	if len(p.Options) == 0 || len(p.Options) > askMaxOptions {
		return nil, invalidParam("options", "options are the answers the person picks from with the keys 1 to 9: give between 1 and 9. For a free-form answer, send a message to human instead")
	}
	for i, o := range p.Options {
		if !askLineShown(o, askMaxOption) {
			return nil, invalidParam("options", "option "+strconv.Itoa(i+1)+" must be one line of printable text, at most 60 bytes")
		}
		if slices.Index(p.Options, o) != i {
			return nil, invalidParam("options", "option "+echoName(o)+" is given twice")
		}
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	item := AttentionItem{Session: sess.Name, Summary: p.Question, Options: p.Options, Name: "ask-human"}
	target := p.Window
	if fromPane && target == "" {
		target = own
	}
	if target != "" {
		st := sess.GetState()
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		w := st.Windows[idx]
		if fromPane && w.ID != own {
			return nil, hintedVerbError(ErrVerbForbidden, "ask-human from inside a pane asks only as that pane", &VerbHint{
				Param:  "window",
				Detail: "Nothing was asked. Omit window, and the question is asked as your own pane, where the answer comes back.",
			})
		}
		item.Window, item.Name = w.ID, windowLabelOf(w)
		item.Harness, item.Workspace = w.AgentHarness, w.Workspace
	}

	hold, err := d.attention.openAsk(item, wait)
	if err != nil {
		return nil, hintedVerbError(ErrVerbRateLimited, "this session holds "+strconv.Itoa(askMaxOutside)+" open questions asked from outside every pane, which is the cap", &VerbHint{
			Verb:   "list-attention",
			Detail: "Nothing was asked. Wait for the person to answer or dismiss one, then ask again.",
		})
	}
	LogBasic("Question %s put to the person for %s in %s", hold.id, firstNonEmpty(shortID(item.Window), "a caller outside every pane"), sess.Name)
	if !wait {
		return askResult(hold.id, askOutcome{}), nil
	}
	return d.awaitAsk(hold, timeout), nil
}

// rejoinAsk is ask-human with a request id: wait on the question again, or
// read how it ended.
func (d *Daemon) rejoinAsk(requestID string, fromPane bool, own string, wait bool, timeout time.Duration) (any, *verbError) {
	window, known := d.attention.askPane(requestID)
	if !known {
		return nil, hintedVerbError(ErrVerbInvalidParams, "no question was asked under request "+echoName(requestID), &VerbHint{
			Param:  "request_id",
			Verb:   "list-attention",
			Detail: "A question is remembered for a while after it ends, and never across a daemon restart.",
		})
	}
	if fromPane && window != own {
		return nil, hintedVerbError(ErrVerbForbidden, "a caller inside a pane may come back only for its own pane's questions", &VerbHint{
			Param: "request_id",
		})
	}
	hold, out, settled, err := d.attention.joinAsk(requestID)
	switch {
	case errors.Is(err, errAskBusy):
		return nil, invalidParam("request_id", "another call is already waiting on this question")
	case err != nil:
		return nil, invalidParam("request_id", "no question was asked under request "+echoName(requestID))
	case settled:
		return askResult(requestID, out), nil
	}
	if !wait {
		d.attention.leaveAsk(hold)
		return askResult(requestID, askOutcome{}), nil
	}
	return d.awaitAsk(hold, timeout), nil
}

// awaitAsk blocks on an open question until it ends or timeout passes.
func (d *Daemon) awaitAsk(hold *askHold, timeout time.Duration) map[string]any {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case out := <-hold.done:
		return askResult(hold.id, out)
	case <-timer.C:
	case <-d.ctx.Done():
		if out, ended := d.attention.leaveAsk(hold); ended {
			return askResult(hold.id, out)
		}
		return askResult(hold.id, askOutcome{Reason: askEndShutdown})
	}
	if out, ended := d.attention.leaveAsk(hold); ended {
		return askResult(hold.id, out)
	}
	return askResult(hold.id, askOutcome{})
}

// verbAnswerAsk answers a question for the person.
func (d *Daemon) verbAnswerAsk(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		RequestID  string `json:"request_id"`
		Answer     string `json:"answer"`
		HumanNonce string `json:"human_nonce"`
		// Question is the line the answer was picked from. When it is set
		// and the question says something else, nothing is answered.
		Question string `json:"question"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.RequestID == "" {
		return nil, invalidParam("request_id", "request_id is required: the question's, from its Inbox item")
	}
	if p.Answer == "" {
		return nil, invalidParam("answer", "answer is required: one of the question's options")
	}
	by, ok := d.humanNonceClient(p.HumanNonce, cs)
	if !ok {
		return nil, hintedVerbError(ErrVerbNotHuman, "answer-ask is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was answered. Only a client attached right now can answer a question, by passing the nonce its attach reply carried. An agent never can.",
		})
	}
	out, hold, applied, mail, err := d.attention.answerAsk(p.RequestID, p.Answer, by, p.Question)
	switch {
	case errors.Is(err, errNoHold):
		return nil, invalidParam("request_id", "no question is open under request "+echoName(p.RequestID))
	case errors.Is(err, errBadDecision):
		return nil, invalidParam("answer", "this question does not offer "+echoName(p.Answer), hold.options...)
	}
	res := map[string]any{
		"type":       "ask_answered",
		"request_id": p.RequestID,
		"applied":    applied,
		"reason":     out.Reason,
	}
	switch {
	case !applied && out.Reason == approvalEndChanged:
		res["question"] = hold.question
	case !applied:
		res["answer"] = out.Answer
		if out.By != "" {
			res["answered_by"] = out.By
		}
	default:
		res["answer"] = out.Answer
		res["answered_by"] = out.By
		LogBasic("Question %s answered by %s", p.RequestID, by)
		if mail {
			d.mailAskAnswer(hold, out)
		}
	}
	return res, nil
}

// mailAskAnswer hands an answer nobody was waiting for to the asking pane, as
// a message from human. It is verified_human: the daemon checked the attach
// the answer came from before it got here, the check a reply from the mail
// overlay passes.
func (d *Daemon) mailAskAnswer(h askHold, out askOutcome) {
	sess := d.manager.GetSession(h.session)
	if sess == nil {
		return
	}
	to, label, err := resolveMailParty(sess.GetState(), h.window)
	if err != nil {
		return
	}
	stored := d.agents.send(sess.Name, AgentMessage{
		Kind:          agentMsgDirect,
		From:          AgentInboxHuman,
		FromLabel:     AgentInboxHuman,
		To:            to,
		ToLabel:       label,
		Subject:       attentionText("Answer: "+h.question, agentMsgMaxSubject),
		Text:          out.Answer,
		VerifiedHuman: true,
	})
	d.broadcastToSession(sess.ID, MsgAgentMail, &AgentMailPayload{Message: stored}, "")
	d.events.publish(streamEvent{Type: EventAgentMessage, Session: sess.Name, Window: stored.To})
}
