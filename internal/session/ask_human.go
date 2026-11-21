package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"
)

const (
	askMaxOptions = 9
	askMaxOption  = 60
	askMaxOutside = 16
	askSettledMax = 256
)

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

type askOutcome struct {
	Answer string
	Index  int
	By     string
	Reason string
	window string
}

type askHold struct {
	id       string
	itemID   string
	session  string
	window   string
	question string
	options  []string
	waiting  bool
	done     chan askOutcome
}

var (
	errAskBusy    = errors.New("another call is waiting on this question")
	errAskTooMany = errors.New("too many open questions")
)

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

func (a *attentionStore) askSession(requestID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h, ok := a.asks[requestID]; ok {
		return h.session, true
	}
	return "", false
}

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

func askLineShown(s string, limit int) bool {
	return len(s) <= limit && approvalLineShown(s)
}

func askLinePrintable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || InvisibleFormatRune(r) {
			return false
		}
	}
	return true
}

func askResult(requestID string, out askOutcome) map[string]any {
	status := out.Reason
	if status == "" {
		status = AskPending
	}
	res := map[string]any{
		"type":           "human_answer",
		"request_id":     requestID,
		"status":         status,
		"verified_human": status == AskAnswered,
	}
	if status == AskAnswered {
		res["answer"] = out.Answer
		res["answer_index"] = out.Index
		res["answered_by"] = out.By
	}
	return res
}

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
	timeout := d.askWait
	if p.Timeout > 0 {
		timeout = min(time.Duration(p.Timeout)*time.Millisecond, maxAskWait)
	}
	fromPane, own := d.peerPane(cs)

	if p.RequestID != "" {
		if p.Question != "" || len(p.Options) > 0 {
			return nil, invalidParam("request_id", "request_id comes back for a question already asked, so it takes no question or options")
		}
		return d.rejoinAsk(cs, p.RequestID, fromPane, own, wait, timeout)
	}

	if len(p.Question) > attentionMaxSummary && askLinePrintable(p.Question) {
		mail := "tuios send-agent-message -w human \"<the details>\""
		if sess, verr := d.resolveVerbSession(cs, p.Session); verr == nil {
			mail = "tuios send-agent-message -s " + shquote.Word(sess.Name()) + " -w human \"<the details>\""
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, "the question is "+strconv.Itoa(len(p.Question))+" bytes, and the limit is "+strconv.Itoa(attentionMaxSummary)+" bytes", &VerbHint{
			Param:   "question",
			Command: mail,
			Detail:  "Nothing was asked. Send the details to the person in a message, then ask a short question.",
		})
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

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	item := AttentionItem{Session: sess.Name(), Summary: p.Question, Options: p.Options, Name: "ask-human"}
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
	LogBasic("Question %s put to the person for %s in %s", hold.id, firstNonEmpty(shortID(item.Window), "a caller outside every pane"), sess.Name())
	if !wait {
		return askResult(hold.id, askOutcome{}), nil
	}
	return d.awaitAsk(cs, hold, timeout, fromPane), nil
}

func (d *Daemon) rejoinAsk(cs *connState, requestID string, fromPane bool, own string, wait bool, timeout time.Duration) (any, *verbError) {
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
	return d.awaitAsk(cs, hold, timeout, fromPane), nil
}

func (d *Daemon) awaitAsk(cs *connState, hold *askHold, timeout time.Duration, fromPane bool) map[string]any {
	if fromPane && hold.window != "" {
		if sess, _ := d.manager.ResolveSession(hold.session); sess != nil {
			sess.showAsk(hold.window, hold.id, hold.question)
			defer func() {
				for _, s := range d.manager.AllSessions() {
					s.endAskShow(hold.window, hold.id)
				}
			}()
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case out := <-hold.done:
		return d.askReply(cs, hold, out)
	case <-timer.C:
	case <-d.ctx.Done():
		if out, ended := d.attention.leaveAsk(hold); ended {
			return d.askReply(cs, hold, out)
		}
		return askResult(hold.id, askOutcome{Reason: askEndShutdown})
	}
	if out, ended := d.attention.leaveAsk(hold); ended {
		return d.askReply(cs, hold, out)
	}
	return askResult(hold.id, askOutcome{})
}

func (d *Daemon) askReply(cs *connState, hold *askHold, out askOutcome) map[string]any {
	if out.Reason == AskAnswered && hold.window != "" && cs != nil {
		h := askHold{id: hold.id, session: hold.session, window: hold.window, question: hold.question}
		cs.replyFailed = func() {
			LogBasic("Question %s: the caller left before its answer, mailing it to the pane", hold.id)
			d.mailAskAnswer(h, out)
		}
	}
	return askResult(hold.id, out)
}

func (d *Daemon) verbAnswerAsk(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		RequestID     string `json:"request_id"`
		Answer        string `json:"answer"`
		HumanNonce    string `json:"human_nonce"`
		Question      string `json:"question"`
		UpgradeDialog bool   `json:"upgrade_dialog"`
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
	if !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "answer-ask is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was answered. Only a client attached right now can answer a question, by passing the nonce its attach reply carried. An agent never can.",
		})
	}
	if d.attention.askIsUpgrade(p.RequestID) {
		if cs != nil && cs.viaLink {
			return nil, hintedVerbError(ErrVerbNotHuman, "a live upgrade is confirmed only at a client attached on this machine", &VerbHint{
				Detail: "Nothing was answered. Answer it in the dialog of a client attached to this machine.",
			})
		}
		if !p.UpgradeDialog {
			return nil, hintedVerbError(ErrVerbNotHuman, "a live upgrade is confirmed only by a key pressed in the upgrade dialog", &VerbHint{
				Param:  "upgrade_dialog",
				Detail: "Nothing was answered. Answer it in the dialog that a tuios client attached to this machine shows.",
			})
		}
	}
	askSession, _ := d.attention.askSession(p.RequestID)
	by, ok := d.humanNonceFor(p.HumanNonce, d.sessionIDOf(askSession), cs)
	if !ok {
		return nil, nonceScopeError("answer-ask")
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

func (d *Daemon) mailAskAnswer(h askHold, out askOutcome) {
	sess, _ := d.manager.ResolveSession(h.session)
	if sess == nil {
		return
	}
	to, label, err := resolveMailParty(sess.GetState(), h.window)
	if err != nil {
		return
	}
	stored := d.agents.send(sess.Name(), AgentMessage{
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
	d.events.publish(streamEvent{Type: EventAgentMessage, Session: sess.Name(), Window: stored.To})
}
