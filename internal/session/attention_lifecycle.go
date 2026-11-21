package session

import (
	"encoding/json"
	"slices"
	"time"
)

// SnoozeUntilChange is the snoozed_until of an item that sleeps until its fact
// changes rather than until a time.
const SnoozeUntilChange int64 = -1

func (a *attentionStore) undoSpan() time.Duration {
	return positiveOr(a.undoWindow, attentionUndoWindow)
}

const attentionUndoWindow = 10 * time.Second

const attentionUndoMax = 16

const attentionSnoozeMax = 366 * 24 * time.Hour

type attentionUndo struct {
	item   AttentionItem
	reason string
	at     time.Time
}

func attentionSnoozable(it *AttentionItem) (bool, string) {
	switch it.Kind {
	case AttentionFinished, AttentionErrored, AttentionMail, AttentionResume, AttentionQuestion:
		return true, ""
	case AttentionApproval:
		if it.RequestID != "" {
			return false, "tuios holds this approval for an answer: answer it or dismiss it"
		}
		return true, ""
	case AttentionPlan:
		return false, "a plan waits for an answer: answer it or dismiss it"
	case AttentionAsk:
		return false, "a question put with ask-human waits for an answer: answer it or dismiss it"
	case AttentionOutbox:
		return false, "mail waiting for another machine goes when the link is back; dismiss it to discard it"
	}
	return false, "this kind of item cannot be snoozed"
}

func (a *attentionStore) openOrSnoozedLocked(key string) *AttentionItem {
	if id, ok := a.byKey[key]; ok {
		return a.items[id]
	}
	if id, ok := a.snoozedKey[key]; ok {
		return a.snoozed[id]
	}
	return nil
}

func (a *attentionStore) snoozedIDsLocked() []string {
	ids := make([]string, 0, len(a.snoozed))
	for id := range a.snoozed {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y string) int {
		sx, sy := a.snoozed[x].Seq, a.snoozed[y].Seq
		switch {
		case sx < sy:
			return -1
		case sx > sy:
			return 1
		}
		return 0
	})
	return ids
}

func (a *attentionStore) putSnoozedLocked(it *AttentionItem) {
	if a.snoozed == nil {
		a.snoozed = make(map[string]*AttentionItem)
		a.snoozedKey = make(map[string]string)
	}
	a.snoozed[it.ID] = it
	a.snoozedKey[attentionItemKey(it)] = it.ID
}

func (a *attentionStore) takeSnoozedLocked(id string) (*AttentionItem, bool) {
	it, ok := a.snoozed[id]
	if !ok {
		return nil, false
	}
	delete(a.snoozed, id)
	delete(a.snoozedKey, attentionItemKey(it))
	return it, true
}

func (a *attentionStore) snoozeLocked(id string, until int64) (AttentionItem, *verbError) {
	if it, ok := a.hostItems[id]; ok {
		if ok, why := attentionSnoozable(it); !ok {
			return AttentionItem{}, invalidParam("id", why)
		}
		out := *it
		delete(a.hostItems, id)
		out.SnoozedUntil = until
		if a.hostSnoozed == nil {
			a.hostSnoozed = make(map[string]*AttentionItem)
		}
		asleep := out
		a.hostSnoozed[id] = &asleep
		a.rev++
		closed := out
		closed.Seq = a.rev
		closed.Closed = AttentionClosedSnoozed
		a.publish(attentionEvent(AttentionClosed, closed))
		a.rememberUndoLocked(out, AttentionClosedSnoozed)
		a.armWakeLocked()
		return closed, nil
	}
	it, ok := a.items[id]
	if !ok {
		return AttentionItem{}, attentionNoItem(id)
	}
	if ok, why := attentionSnoozable(it); !ok {
		return AttentionItem{}, invalidParam("id", why)
	}
	if len(a.snoozed) >= attentionMaxItems {
		return AttentionItem{}, invalidParam("id", "too many items are snoozed already; wake or dismiss some")
	}
	delete(a.items, id)
	delete(a.byKey, attentionItemKey(it))
	it.SnoozedUntil = until
	held := it.WithParentUntil != 0
	it.WithParentUntil = 0
	a.putSnoozedLocked(it)
	a.rev++
	closed := *it
	closed.Seq = a.rev
	closed.Closed = AttentionClosedSnoozed
	a.publish(attentionEvent(AttentionClosed, closed))
	a.changedLocked()
	undo := *it
	undo.SnoozedUntil = 0
	a.rememberUndoLocked(undo, AttentionClosedSnoozed)
	a.armWakeLocked()
	if held {
		a.armParentLocked()
	}
	return closed, nil
}

func (a *attentionStore) wakeLocked(id string) (AttentionItem, bool) {
	if it, ok := a.hostSnoozed[id]; ok {
		delete(a.hostSnoozed, id)
		it.SnoozedUntil = 0
		a.rev++
		it.Seq = a.rev
		a.hostItems[id] = it
		a.publish(attentionEvent(AttentionOpened, *it))
		a.armWakeLocked()
		return *it, true
	}
	it, ok := a.takeSnoozedLocked(id)
	if !ok {
		return AttentionItem{}, false
	}
	it.SnoozedUntil = 0
	a.rev++
	it.Seq = a.rev
	a.items[it.ID] = it
	a.byKey[attentionItemKey(it)] = it.ID
	a.publish(attentionEvent(AttentionOpened, *it))
	a.changedLocked()
	a.armWakeLocked()
	return *it, true
}

func (a *attentionStore) wakeOnChangeLocked(key string, next AttentionItem) bool {
	id, ok := a.snoozedKey[key]
	if !ok {
		return false
	}
	cur := a.snoozed[id]
	probe := next
	probe.SnoozedUntil, probe.MarkedUnread = cur.SnoozedUntil, cur.MarkedUnread
	if attentionSame(*cur, probe) {
		return true
	}
	a.takeSnoozedLocked(id)
	next.ID, next.Since = cur.ID, cur.Since
	next.SnoozedUntil = 0
	a.rev++
	next.Seq = a.rev
	it := next
	a.items[it.ID] = &it
	a.byKey[key] = it.ID
	a.publish(attentionEvent(AttentionOpened, it))
	a.changedLocked()
	a.armWakeLocked()
	return true
}

func (a *attentionStore) dropSnoozedLocked(id, reason string) {
	it, ok := a.takeSnoozedLocked(id)
	if !ok {
		return
	}
	a.rev++
	closed := *it
	closed.Seq = a.rev
	closed.Closed = reason
	a.publish(attentionEvent(AttentionClosed, closed))
	a.changedLocked()
	a.armWakeLocked()
}

func (a *attentionStore) dropHostSnoozedLocked(id, reason string) {
	it, ok := a.hostSnoozed[id]
	if !ok {
		return
	}
	delete(a.hostSnoozed, id)
	a.rev++
	closed := *it
	closed.Seq = a.rev
	closed.Closed = reason
	a.publish(attentionEvent(AttentionClosed, closed))
	a.armWakeLocked()
}

func (a *attentionStore) armWakeLocked() {
	var next int64
	for _, m := range []map[string]*AttentionItem{a.snoozed, a.hostSnoozed} {
		for _, it := range m {
			if it.SnoozedUntil > 0 && (next == 0 || it.SnoozedUntil < next) {
				next = it.SnoozedUntil
			}
		}
	}
	if next == a.wakeAt && (next == 0) == (a.wakeTimer == nil) {
		return
	}
	if a.wakeTimer != nil {
		a.wakeTimer.Stop()
		a.wakeTimer = nil
	}
	a.wakeGen++
	a.wakeAt = next
	if next == 0 {
		return
	}
	delay := time.Duration(next - a.clock().UnixNano())
	gen := a.wakeGen
	a.wakeTimer = time.AfterFunc(max(delay, 0), func() { a.wakeFired(gen) })
}

func (a *attentionStore) wakeFired(gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.wakeGen {
		return
	}
	a.wakeDueLocked()
}

func (a *attentionStore) wakeDueLocked() {
	if a.wakeTimer != nil {
		a.wakeTimer.Stop()
	}
	a.wakeTimer, a.wakeAt = nil, 0
	a.wakeGen++
	now := a.clock().UnixNano()
	var due []string
	for _, m := range []map[string]*AttentionItem{a.snoozed, a.hostSnoozed} {
		for id, it := range m {
			if it.SnoozedUntil > 0 && it.SnoozedUntil <= now {
				due = append(due, id)
			}
		}
	}
	slices.Sort(due)
	for _, id := range due {
		a.wakeLocked(id)
	}
	a.armWakeLocked()
}

func (a *attentionStore) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *attentionStore) rememberUndoLocked(it AttentionItem, reason string) {
	if it.Kind == AttentionOutbox || it.Kind == AttentionAsk {
		return
	}
	now := a.clock()
	a.undo = slices.DeleteFunc(a.undo, func(u attentionUndo) bool {
		return u.item.ID == it.ID || now.Sub(u.at) >= a.undoSpan()
	})
	it.RequestID, it.Options, it.Expires, it.AlwaysScope = "", nil, 0, nil
	if it.Host == "" {
		if it.Kind == AttentionPlan {
			it.Kind = AttentionApproval
		}
		it.DenyMessage, it.PlanLines, it.PlanSHA = false, 0, ""
	}
	it.Closed = ""
	a.undo = append(a.undo, attentionUndo{item: it, reason: reason, at: now})
	if len(a.undo) > attentionUndoMax {
		a.undo = a.undo[len(a.undo)-attentionUndoMax:]
	}
}

func (a *attentionStore) undoEntry(id string) (attentionUndo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.undoEntryLocked(id)
}

func (a *attentionStore) undoEntryLocked(id string) (attentionUndo, bool) {
	now := a.clock()
	for _, u := range a.undo {
		if u.item.ID == id && now.Sub(u.at) < a.undoSpan() {
			return u, true
		}
	}
	return attentionUndo{}, false
}

func (a *attentionStore) restore(id string) (AttentionItem, *verbError) {
	a.mu.Lock()
	hook := a.beforeRestore
	a.beforeRestore = nil
	a.mu.Unlock()
	if hook != nil {
		hook()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.undoEntryLocked(id)
	if !ok {
		return AttentionItem{}, invalidParam("id", "nothing dismissed or snoozed in the last 10 seconds has id "+echoName(id))
	}
	a.undo = slices.DeleteFunc(a.undo, func(x attentionUndo) bool { return x.item.ID == id })
	if u.reason == AttentionClosedSnoozed {
		if it, ok := a.wakeLocked(id); ok {
			return it, nil
		}
		return AttentionItem{}, invalidParam("id", "item "+echoName(id)+" is not asleep any more: it woke, or what it was about is over")
	}
	it := u.item
	it.WithParentUntil = 0
	if it.Host != "" {
		if _, hidden := a.hostHidden[id]; !hidden {
			return AttentionItem{}, invalidParam("id", "the machine it came from changed or closed item "+echoName(id)+" since")
		}
		if _, open := a.hostItems[id]; open {
			return AttentionItem{}, invalidParam("id", "item "+echoName(id)+" is open")
		}
		delete(a.hostHidden, id)
		a.rev++
		it.Seq = a.rev
		item := it
		a.hostItems[id] = &item
		a.publish(attentionEvent(AttentionOpened, item))
		return item, nil
	}
	key := attentionItemKey(&it)
	if a.openOrSnoozedLocked(key) != nil {
		return AttentionItem{}, invalidParam("id", "a newer item about the same thing is in the Inbox")
	}
	if _, taken := a.items[id]; taken {
		return AttentionItem{}, invalidParam("id", "item "+echoName(id)+" is open")
	}
	a.rev++
	it.Seq = a.rev
	item := it
	a.items[id] = &item
	a.byKey[key] = id
	a.publish(attentionEvent(AttentionOpened, item))
	a.changedLocked()
	return item, nil
}

func (a *attentionStore) openUnread(it AttentionItem) AttentionItem {
	a.mu.Lock()
	defer a.mu.Unlock()
	it.Kind = AttentionFinished
	it.MarkedUnread = true
	key := attentionItemKey(&it)
	if id, ok := a.snoozedKey[key]; ok {
		a.wakeLocked(id)
	}
	if id, ok := a.byKey[key]; ok {
		cur := a.items[id]
		it.Count = max(cur.Count, 1)
		if it.Summary == "" {
			it.Summary = cur.Summary
		}
	}
	a.upsertLocked(it)
	id := a.byKey[key]
	return *a.items[id]
}

func (a *attentionStore) resolveMarkTargetLocked(p markAttentionParams) (string, bool) {
	if p.ID != "" {
		_, open := a.items[p.ID]
		_, asleep := a.snoozed[p.ID]
		_, host := a.hostItems[p.ID]
		_, hostAsleep := a.hostSnoozed[p.ID]
		return p.ID, open || asleep || host || hostAsleep
	}
	key := attentionKey(p.Kind, p.Session, p.Window, 0)
	if id, ok := a.byKey[key]; ok && a.items[id].Kind == p.Kind {
		return id, true
	}
	if id, ok := a.snoozedKey[key]; ok && a.snoozed[id].Kind == p.Kind {
		return id, true
	}
	return "", false
}

func attentionNoItem(id string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, "no attention item has id "+echoName(id), &VerbHint{
		Param:   "id",
		Verb:    "list-attention",
		Command: "tuios list-attention --snoozed",
		Detail:  "The item may already be closed: its pane moved on, the mail was read, or someone else dismissed it.",
	})
}

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

func (p markAttentionParams) snoozeUntil(now time.Time) (int64, *verbError) {
	given := 0
	for _, set := range []bool{p.Until != 0, p.ForMS != 0, p.UntilChange} {
		if set {
			given++
		}
	}
	if given != 1 {
		return 0, invalidParam("for_ms", "snooze takes exactly one of until, for_ms and until_change")
	}
	switch {
	case p.UntilChange:
		return SnoozeUntilChange, nil
	case p.ForMS != 0:
		d := time.Duration(p.ForMS) * time.Millisecond
		if p.ForMS < 0 || d > attentionSnoozeMax {
			return 0, invalidParam("for_ms", "for_ms is a positive number of milliseconds, at most a year")
		}
		return now.Add(d).UnixNano(), nil
	}
	at := time.UnixMilli(p.Until)
	if !at.After(now) || at.Sub(now) > attentionSnoozeMax {
		return 0, invalidParam("until", "until is a time in the next year, in unix milliseconds")
	}
	return at.UnixNano(), nil
}

func (d *Daemon) verbMarkAttention(cs *connState, params json.RawMessage) (any, *verbError) {
	var p markAttentionParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(markAttentionActions, p.Action) {
		return nil, invalidParam("action", "action is one of snooze, wake, unread, restore, settle and unsettle", markAttentionActions...)
	}
	settle := p.Action == "settle" || p.Action == "unsettle"
	if p.ID == "" && !settle && (p.Window == "" || (p.Kind == "" && p.Action != "unread")) {
		return nil, invalidParam("id", "name the item with id, or with session, window and kind")
	}
	if !d.humanNonceHeld(p.HumanNonce, cs) {
		return nil, hintedVerbError(ErrVerbNotHuman, "mark-attention is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Only a client attached right now can snooze, wake, mark unread, restore or settle, by passing the nonce its attach reply carried. An agent cannot change what the person reads.",
		})
	}
	if p.Action != "snooze" && (p.Until != 0 || p.ForMS != 0 || p.UntilChange) {
		return nil, invalidParam("until", "until, for_ms and until_change go with snooze only")
	}
	if p.Kind != "" && AttentionKindRank(p.Kind) == len(AttentionKindNames) {
		return nil, invalidParam("kind", "kind is an attention kind", AttentionKindNames...)
	}
	switch p.Action {
	case "settle", "unsettle":
		return d.markAttentionSettle(cs, p)
	case "unread":
		return d.markAttentionUnread(cs, p)
	case "restore":
		return d.markAttentionRestore(p)
	}
	if p.ID == "" {
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		idx, err := findWindowStateIndex(sess.GetState().Windows, p.Window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		p.Session, p.Window = sess.Name(), sess.GetState().Windows[idx].ID
	}
	target := d.sessionIDOf(p.Session)
	if p.ID != "" {
		target = d.attentionItemSession(p.ID)
	}
	if _, ok := d.humanNonceFor(p.HumanNonce, target, cs); !ok {
		return nil, nonceScopeError("mark-attention")
	}
	a := d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.resolveMarkTargetLocked(p)
	if !ok {
		if p.ID != "" {
			return nil, attentionNoItem(p.ID)
		}
		return nil, invalidParam("kind", "the pane has no "+echoName(p.Kind)+" item in the Inbox")
	}
	switch p.Action {
	case "snooze":
		until, verr := p.snoozeUntil(a.clock())
		if verr != nil {
			return nil, verr
		}
		if _, asleep := a.snoozed[id]; asleep {
			a.snoozed[id].SnoozedUntil = until
			a.changedLocked()
			a.armWakeLocked()
		} else if it, asleep := a.hostSnoozed[id]; asleep {
			it.SnoozedUntil = until
			a.armWakeLocked()
		} else if _, verr := a.snoozeLocked(id, until); verr != nil {
			return nil, verr
		}
		return map[string]any{"type": "attention_marked", "id": id, "action": p.Action, "snoozed_until": until}, nil
	default:
		if _, ok := a.wakeLocked(id); !ok {
			return nil, invalidParam("id", "item "+echoName(id)+" is not snoozed")
		}
		return map[string]any{"type": "attention_marked", "id": id, "action": p.Action}, nil
	}
}

func (d *Daemon) markAttentionUnread(cs *connState, p markAttentionParams) (any, *verbError) {
	if p.Kind != "" && p.Kind != AttentionFinished {
		return nil, invalidParam("kind", "unread reopens a finished turn, so kind is finished or left out", AttentionFinished)
	}
	sessionName, window := p.Session, p.Window
	if p.ID != "" {
		it, ok := d.attentionItemAnywhere(p.ID)
		if !ok {
			return nil, attentionNoItem(p.ID)
		}
		if it.Host != "" {
			return nil, invalidParam("id", "unread acts on this machine's panes: attach to "+echoName(it.Host)+" to mark its panes")
		}
		sessionName, window = it.Session, it.Window
	}
	sess, verr := d.resolveVerbSession(cs, sessionName)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	if w.CompletionSeq == 0 {
		return nil, invalidParam("window", "the pane has not finished a turn, so there is nothing to mark unread")
	}
	if !agentStateFinishes(w.AgentState) && w.AgentState != AgentStateNone {
		return nil, invalidParam("window", "the agent in the pane is "+w.AgentState.Name()+" now; a turn it finishes shows up by itself")
	}
	sess.MarkCompletionUnseen(w.ID)
	it := d.attention.openUnread(AttentionItem{
		Session:       sess.Name(),
		Window:        w.ID,
		Workspace:     w.Workspace,
		Harness:       w.AgentHarness,
		Name:          attentionText(windowLabelOf(w), attentionMaxSummary),
		Summary:       attentionText(w.AgentMessage, attentionMaxSummary),
		CompletionSeq: w.CompletionSeq,
		Count:         1,
	})
	return map[string]any{"type": "attention_marked", "id": it.ID, "action": p.Action}, nil
}

func (d *Daemon) markAttentionRestore(p markAttentionParams) (any, *verbError) {
	if p.ID == "" {
		return nil, invalidParam("id", "restore takes the id of the item dismissed or snoozed")
	}
	u, ok := d.attention.undoEntry(p.ID)
	if !ok {
		return nil, invalidParam("id", "nothing dismissed or snoozed in the last 10 seconds has id "+echoName(p.ID))
	}
	var sess *Session
	var it AttentionItem
	var verr *verbError
	if u.item.Host == "" && u.reason == AttentionClosedDismissed {
		sess = d.manager.GetSession(u.item.Session)
		if sess == nil {
			return nil, invalidParam("id", "the session of item "+echoName(p.ID)+" ended")
		}
		if u.item.Window != "" && u.item.Kind != AttentionMail {
			sess.withWindowState(u.item.Window, func(w WindowState, found bool) {
				if !found {
					verr = invalidParam("id", "the pane of item "+echoName(p.ID)+" closed")
					return
				}
				if want := attentionKindState(u.item.Kind); want != "" && w.AgentState.Name() != want {
					verr = invalidParam("id", "the pane moved on since: it is "+w.AgentState.Name()+" now")
					return
				}
				it, verr = d.attention.restore(p.ID)
			})
		} else {
			it, verr = d.attention.restore(p.ID)
		}
	} else {
		it, verr = d.attention.restore(p.ID)
	}
	if verr != nil {
		return nil, verr
	}
	if sess != nil && it.Kind == AttentionFinished {
		sess.MarkCompletionUnseen(it.Window)
	}
	return map[string]any{"type": "attention_marked", "id": it.ID, "action": p.Action}, nil
}

func attentionKindState(kind string) string {
	switch kind {
	case AttentionApproval, AttentionQuestion, AttentionPlan:
		return AgentStateNeedsInput.Name()
	case AttentionErrored:
		return AgentStateErrored.Name()
	}
	return ""
}

func (d *Daemon) attentionItemAnywhere(id string) (AttentionItem, bool) {
	a := d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range []map[string]*AttentionItem{a.items, a.snoozed, a.hostItems, a.hostSnoozed} {
		if it, ok := m[id]; ok {
			return *it, true
		}
	}
	return AttentionItem{}, false
}

func (d *Daemon) attentionItemSession(id string) string {
	it, ok := d.attentionItemAnywhere(id)
	if !ok || it.Host != "" {
		return ""
	}
	return d.sessionIDOf(it.Session)
}
