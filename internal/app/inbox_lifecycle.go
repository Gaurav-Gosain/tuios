package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The Inbox's lifecycle beyond answering and dismissing: snoozing an item,
// undoing a dismiss or a snooze made a moment ago, showing what is snoozed,
// marking a finished pane unread from the rail, and walking the unseen
// finished turns newest first (ctrl+b O). Each is the person's act, sent with
// mark-attention and the attach nonce, so the daemon can tell the person from
// an agent (see session/attention_lifecycle.go).
//
// Nothing here runs on its own. There is no timer on the client: a snoozed
// item wakes on the daemon, which publishes it open again, and the mirror
// takes it back into the list from that event.

// inboxUndoWindow is how long after a dismiss or a snooze u restores it. It is
// the daemon's window too.
const inboxUndoWindow = 10 * time.Second

// inboxUndoNotice is how long, at least, the dock says a dismiss or a snooze
// can be undone. The dock's own floor may keep it up longer.
const inboxUndoNotice = 5 * time.Second

// inboxUndoMax bounds the closes kept for u.
const inboxUndoMax = 16

// inboxSnoozeChoice is one length the snooze picker offers.
type inboxSnoozeChoice struct {
	key   string
	label string
	// words say how long in a sentence: "for 15 minutes".
	words string
	// params are what mark-attention takes for it, given the time now.
	params func(now time.Time) map[string]any
}

// inboxSnoozeChoices are the four lengths, on the digits 1 to 4.
var inboxSnoozeChoices = []inboxSnoozeChoice{
	{"1", "15m", "for 15 minutes", func(time.Time) map[string]any { return map[string]any{"for_ms": (15 * time.Minute).Milliseconds()} }},
	{"2", "1h", "for an hour", func(time.Time) map[string]any { return map[string]any{"for_ms": time.Hour.Milliseconds()} }},
	{"3", "tomorrow 9:00", "until 9:00 tomorrow", func(now time.Time) map[string]any {
		return map[string]any{"until": nextMorning(now).UnixMilli()}
	}},
	{"4", "until it changes", "until it changes", func(time.Time) map[string]any { return map[string]any{"until_change": true} }},
}

// nextMorning is 9:00 tomorrow, local time.
func nextMorning(now time.Time) time.Time {
	y, mo, d := now.Date()
	return time.Date(y, mo, d+1, 9, 0, 0, 0, now.Location())
}

// inboxLifecycle is the client's half of the lifecycle.
type inboxLifecycle struct {
	// Snoozed are the items the person snoozed, from the listing and from the
	// snoozed closes that followed it, in Inbox order.
	Snoozed []session.AttentionItem
	// ShowSnoozed lists them under the open items.
	ShowSnoozed bool
	// pickFor is the item the snooze picker is open for, empty when it is
	// not. closeAfterPick closes the Inbox once a length is picked, for a
	// picker the rail opened.
	pickFor        string
	closeAfterPick bool
	// undo are this client's dismisses and snoozes, newest last.
	undo []inboxUndoEntry
	// walk is the ctrl+b O walk in progress.
	walk inboxFinishedWalk
	// asked are the items (by id, or by "unread:" and the window for an
	// unread) this client just asked the daemon to open, with when, so their
	// open raises no alert.
	asked map[string]time.Time
	// noMark is set when the daemon holding the Inbox was found without
	// mark-attention: its list-verbs, probed once per watch, did not list
	// it, or a mark came back unknown_verb. The keys that need the verb are
	// then not offered and do what an unbound key does, and a dismiss does
	// not offer an undo it could not make. False means supported or not yet
	// known, which is what a client with a newer daemon, or a test with a
	// fake caller, should see.
	noMark bool
}

// inboxMarkSupported reports whether the daemon holding the Inbox can take
// mark-attention, as far as this client knows.
func (m *OS) inboxMarkSupported() bool {
	return !m.Inbox.life.noMark
}

// probeMarkAttention asks a daemon's list-verbs whether it has
// mark-attention. known is false when the probe itself failed for a reason
// that says nothing about the verb, and the caller then assumes nothing.
func probeMarkAttention(call func(verb string, params map[string]any) ([]byte, error)) (supported, known bool) {
	raw, err := call("list-verbs", map[string]any{"verb": "mark-attention"})
	if err != nil {
		var callErr *session.VerbCallError
		if errors.As(err, &callErr) && (callErr.Code == session.ErrVerbUnknownVerb || callErr.Code == session.ErrVerbInvalidParams) {
			return false, true
		}
		return false, false
	}
	var res struct {
		Verbs []struct {
			Verb string `json:"verb"`
		} `json:"verbs"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return false, false
	}
	for _, v := range res.Verbs {
		if v.Verb == "mark-attention" {
			return true, true
		}
	}
	return false, true
}

// inboxAskedWindow is how long an item this client asked to open is kept
// from alerting when it does.
const inboxAskedWindow = 10 * time.Second

// noteInboxAsked records that this client asked for key's item to open.
func (m *OS) noteInboxAsked(key string) {
	life := &m.Inbox.life
	if life.asked == nil {
		life.asked = make(map[string]time.Time)
	}
	now := time.Now()
	for k, at := range life.asked {
		if now.Sub(at) >= inboxAskedWindow {
			delete(life.asked, k)
		}
	}
	life.asked[key] = now
}

// inboxAskedFor reports, once, whether this client asked for an item that
// opened: woke or restored it by id, or marked its pane unread. An unread
// asks for the pane's finished item only, so another kind opening on the
// same pane, an approval say, still alerts.
func (m *OS) inboxAskedFor(it session.AttentionItem) bool {
	life := &m.Inbox.life
	keys := []string{it.ID}
	if it.Kind == session.AttentionFinished && it.Window != "" {
		keys = append(keys, inboxUnreadKey(it.Window))
	}
	for _, key := range keys {
		if at, ok := life.asked[key]; ok {
			delete(life.asked, key)
			if time.Since(at) < inboxAskedWindow {
				return true
			}
		}
	}
	return false
}

// inboxUnreadKey is the asked key of an unread sent for a pane.
func inboxUnreadKey(windowID string) string { return "unread:" + windowID }

// settleInboxUnreadAsk is the answer to an unread arriving. The daemon names
// the item it opened or marked. One already in the list was marked in place
// and its update raised no alert, so there is nothing left to keep quiet; one
// not yet in the list is still to open, and is kept quiet by its id. Either
// way the pane's key goes, so it cannot silence a later item on that pane.
func (m *OS) settleInboxUnreadAsk(windowID, id string) {
	life := &m.Inbox.life
	if windowID != "" {
		delete(life.asked, inboxUnreadKey(windowID))
	}
	if id == "" || slices.ContainsFunc(m.Inbox.Items, func(it session.AttentionItem) bool { return it.ID == id }) {
		return
	}
	m.noteInboxAsked(id)
}

// inboxUndoEntry is one close u can restore.
type inboxUndoEntry struct {
	id  string
	who string
	at  time.Time
}

// inboxFinishedWalk is where a run of ctrl+b O presses has got to.
type inboxFinishedWalk struct {
	lastSeq uint64
	newest  uint64
	at      time.Time
}

// InboxMarkedMsg is the answer to a mark-attention the Inbox or the rail sent.
type InboxMarkedMsg struct {
	Action string
	ID     string
	Who    string
	// Window is the pane an unread was for.
	Window string
	// Words say what was done, for the dock: "for an hour".
	Words string
	Err   error
}

// inboxSnoozedItem is a snoozed item in the mirror, by id.
func (m *OS) inboxSnoozedIndex(id string) int {
	for i := range m.Inbox.life.Snoozed {
		if m.Inbox.life.Snoozed[i].ID == id {
			return i
		}
	}
	return -1
}

// splitSnoozed takes the snoozed items out of a listing, which lists them
// after the open ones when asked with include_snoozed.
func splitSnoozed(items []session.AttentionItem) (open, snoozed []session.AttentionItem) {
	for _, it := range items {
		if it.SnoozedUntil != 0 {
			snoozed = append(snoozed, it)
		} else {
			open = append(open, it)
		}
	}
	return open, snoozed
}

// applySnoozedEvent folds one event into the snoozed mirror and reports
// whether the event was about a snoozed item and needs nothing more: a snooze
// moves the item from the list to the snoozed ones; an open of a snoozed item
// takes it back (the caller then adds it to the list); a close of a snoozed
// item drops it.
func (m *OS) applySnoozedEvent(action string, it session.AttentionItem) bool {
	life := &m.Inbox.life
	idx := m.inboxSnoozedIndex(it.ID)
	switch {
	case action == session.AttentionClosed && it.Closed == session.AttentionClosedSnoozed:
		it.Closed = ""
		if idx >= 0 {
			life.Snoozed[idx] = it
		} else {
			life.Snoozed = append(life.Snoozed, it)
		}
		session.SortAttention(life.Snoozed)
		return false
	case idx < 0:
		return false
	case action == session.AttentionClosed:
		life.Snoozed = slices.Delete(life.Snoozed, idx, idx+1)
		return true
	default:
		life.Snoozed = slices.Delete(life.Snoozed, idx, idx+1)
		return false
	}
}

// inboxCanMark reports whether this client can send the person's acts to the
// daemon holding the Inbox, and says why not when it cannot.
func (m *OS) inboxCanMark(what string) bool {
	if m.Inbox.call != nil {
		return true
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		m.ShowNotification(what+" needs a client attached to this machine's daemon", "info", m.Settings.NotificationDuration)
		return false
	}
	return true
}

// inboxMarkCmd sends mark-attention with this client's attach nonce.
func (m *OS) inboxMarkCmd(params map[string]any, who, words string) tea.Cmd {
	nonce := m.inboxNonce()
	action, _ := params["action"].(string)
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	params["human_nonce"] = nonce
	switch action {
	case "wake", "restore":
		if id, _ := params["id"].(string); id != "" {
			m.noteInboxAsked(id)
		}
	case "unread":
		if w, _ := params["window"].(string); w != "" {
			m.noteInboxAsked(inboxUnreadKey(w))
		}
	}
	window, _ := params["window"].(string)
	call := m.inboxCaller()
	return func() tea.Msg {
		raw, err := call("mark-attention", params, 5*time.Second)
		msg := InboxMarkedMsg{Action: action, Who: who, Words: words, Window: window, Err: err}
		if err == nil {
			msg.ID = markedID(raw)
		}
		return msg
	}
}

// applyInboxMarked says what happened, and keeps a snooze for u.
func (m *OS) applyInboxMarked(msg InboxMarkedMsg) {
	if msg.Err != nil {
		var callErr *session.VerbCallError
		if errors.As(msg.Err, &callErr) && callErr.Code == session.ErrVerbUnknownVerb {
			m.Inbox.life.noMark = true
			m.ShowNotification("This daemon cannot snooze or mark Inbox items. Restart it with a newer tuios: tuios kill-server", "error", m.Settings.NotificationDuration*2)
			return
		}
		m.ShowNotification(inboxMarkFailWords(msg.Action)+": "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		if msg.Action == "unread" {
			m.settleInboxUnreadAsk(msg.Window, "")
		}
		return
	}
	switch msg.Action {
	case "snooze":
		m.rememberInboxUndo(msg.ID, msg.Who)
		m.ShowNotification("Snoozed "+msg.Who+" "+msg.Words+". "+m.inboxKeyOr(config.ActionInboxUndo, "u")+" in the Inbox undoes.", "info", inboxUndoNotice)
	case "wake":
		m.ShowNotification(capitalize(msg.Who)+" is back in the Inbox", "info", m.Settings.NotificationDuration)
	case "restore":
		m.ShowNotification("Restored "+msg.Who, "success", m.Settings.NotificationDuration)
	case "unread":
		m.settleInboxUnreadAsk(msg.Window, msg.ID)
		m.ShowNotification("Marked "+msg.Who+" unread", "info", m.Settings.NotificationDuration)
	}
}

// inboxMarkFailWords opens the message for a mark that did not go through.
func inboxMarkFailWords(action string) string {
	switch action {
	case "snooze":
		return "The snooze did not go through"
	case "wake":
		return "The item did not wake"
	case "restore":
		return "Nothing was restored"
	case "unread":
		return "The pane was not marked unread"
	}
	return "The Inbox did not change"
}

// markedID is the id a mark-attention answer names.
func markedID(raw []byte) string {
	var res struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &res)
	return res.ID
}

// rememberInboxUndo keeps a close this client's person made, for u.
func (m *OS) rememberInboxUndo(id, who string) {
	if id == "" {
		return
	}
	life := &m.Inbox.life
	now := time.Now()
	life.undo = slices.DeleteFunc(life.undo, func(u inboxUndoEntry) bool {
		return u.id == id || now.Sub(u.at) >= inboxUndoWindow
	})
	life.undo = append(life.undo, inboxUndoEntry{id: id, who: who, at: now})
	if len(life.undo) > inboxUndoMax {
		life.undo = life.undo[len(life.undo)-inboxUndoMax:]
	}
}

// noteInboxDismissed is a dismiss the person made going through: it can be
// undone for a moment, and the dock says so. Mail waiting for another
// machine was discarded and a question put with ask-human was answered as
// dismissed, so neither comes back.
func (m *OS) noteInboxDismissed(id, kind, who string) {
	if kind == session.AttentionOutbox || kind == session.AttentionAsk || !m.inboxMarkSupported() {
		return
	}
	m.rememberInboxUndo(id, who)
	m.ShowNotification("Dismissed "+who+". "+m.inboxKeyOr(config.ActionInboxUndo, "u")+" undoes.", "info", inboxUndoNotice)
}

// InboxSnooze starts a snooze of the selected item: the footer offers the
// four lengths and the next digit picks one. On a snoozed item it wakes it.
func (m *OS) InboxSnooze() (tea.Cmd, bool) {
	if !m.inboxMarkSupported() {
		return nil, false
	}
	it, ok := m.inboxSelected()
	if !ok {
		return nil, true
	}
	if it.SnoozedUntil != 0 {
		if !m.inboxCanMark("Waking an item") {
			return nil, true
		}
		return m.inboxMarkCmd(map[string]any{"id": it.ID, "action": "wake"}, inboxWho(it), ""), true
	}
	if why := inboxSnoozeRefusal(it); why != "" {
		m.ShowNotification(why, "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if !m.inboxCanMark("Snoozing") {
		return nil, true
	}
	m.Inbox.life.pickFor = it.ID
	return nil, true
}

// inboxSnoozeRefusal is why an item cannot be snoozed, empty when it can. It
// is the daemon's rule, said before the person picks a length.
func inboxSnoozeRefusal(it session.AttentionItem) string {
	switch {
	case it.Kind == session.AttentionApproval && it.RequestID != "":
		return "The Inbox is holding this approval for your answer, so it is not snoozed. Answer it or dismiss it."
	case it.Kind == session.AttentionPlan:
		return "A plan waits for your answer, so it is not snoozed. Answer it or dismiss it."
	case it.Kind == session.AttentionAsk:
		return "A question waits for your answer, so it is not snoozed. Answer it or dismiss it."
	case it.Kind == session.AttentionOutbox:
		return "This mail goes when the link is back. Dismiss it to discard it."
	case it.Stale:
		return "That machine cannot be reached, so nothing it shows can be snoozed now."
	}
	return ""
}

// InboxSnoozePicking reports whether the snooze picker is waiting for a
// length.
func (m *OS) InboxSnoozePicking() bool {
	return m.ShowInbox && m.Inbox.life.pickFor != ""
}

// InboxSnoozeKey takes the key pressed while the picker is open: a digit from
// 1 to 4 snoozes for that length, and any other key closes the picker.
func (m *OS) InboxSnoozeKey(key string) tea.Cmd {
	life := &m.Inbox.life
	id := life.pickFor
	life.pickFor = ""
	closeAfter := life.closeAfterPick
	life.closeAfterPick = false
	for _, c := range inboxSnoozeChoices {
		if c.key != key {
			continue
		}
		it, ok := m.inboxItem(id)
		if !ok {
			m.ShowNotification("That item is gone from the Inbox", "info", m.Settings.NotificationDuration)
			return nil
		}
		if closeAfter {
			m.CloseInbox()
		}
		params := c.params(time.Now())
		params["id"], params["action"] = id, "snooze"
		return m.inboxMarkCmd(params, inboxWho(it), c.words)
	}
	if closeAfter {
		m.CloseInbox()
	}
	return nil
}

// inboxSnoozeHints are the footer while the picker waits for a length.
func inboxSnoozeHints() []overlay.Hint {
	hints := make([]overlay.Hint, 0, len(inboxSnoozeChoices)+1)
	for _, c := range inboxSnoozeChoices {
		hints = append(hints, overlay.Hint{Key: c.key, Label: c.label})
	}
	return append(hints, overlay.Hint{Key: "esc", Label: "cancel"})
}

// InboxUndo reopens the item dismissed or snoozed last, within 10 seconds.
// Pressed again it reopens the one before.
func (m *OS) InboxUndo() (tea.Cmd, bool) {
	if !m.inboxMarkSupported() {
		return nil, false
	}
	life := &m.Inbox.life
	now := time.Now()
	life.undo = slices.DeleteFunc(life.undo, func(u inboxUndoEntry) bool { return now.Sub(u.at) >= inboxUndoWindow })
	if len(life.undo) == 0 {
		m.ShowNotification("Nothing to undo: a dismiss or a snooze can be undone for 10 seconds", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if !m.inboxCanMark("Undoing") {
		return nil, true
	}
	u := life.undo[len(life.undo)-1]
	life.undo = life.undo[:len(life.undo)-1]
	return m.inboxMarkCmd(map[string]any{"id": u.id, "action": "restore"}, u.who, ""), true
}

// InboxToggleSnoozed shows or hides the snoozed items under the list.
func (m *OS) InboxToggleSnoozed() (tea.Cmd, bool) {
	if !m.inboxMarkSupported() {
		return nil, false
	}
	life := &m.Inbox.life
	if !life.ShowSnoozed && len(life.Snoozed) == 0 {
		m.ShowNotification("Nothing is snoozed", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	life.ShowSnoozed = !life.ShowSnoozed
	m.clampInboxSelection()
	return nil, true
}

// inboxSnoozedRows are the rows the snoozed items add under the list: a
// muted group while they are shown, or one line saying how many there are
// and the key that shows them. The line comes only under other rows; an
// Inbox with nothing else open says it in its empty state.
func (m *OS) inboxSnoozedRows(rows []inboxRow) []inboxRow {
	life := &m.Inbox.life
	if len(life.Snoozed) == 0 {
		return rows
	}
	if !life.ShowSnoozed {
		if len(rows) > 0 {
			rows = append(rows, inboxRow{note: m.inboxSnoozedNote()})
		}
		return rows
	}
	var items []inboxRow
	for i := range life.Snoozed {
		it := &life.Snoozed[i]
		if inboxFilterAdmits(m.Inbox.Filter, it.Kind) && m.inboxItemSelected(*it) {
			items = append(items, inboxRow{item: it})
		}
	}
	if len(items) == 0 {
		return rows
	}
	rows = append(rows, inboxRow{heading: "Snoozed", count: len(items)})
	return append(rows, items...)
}

// inboxSnoozedNote is the line that says how many items are snoozed.
func (m *OS) inboxSnoozedNote() string {
	n := len(m.Inbox.life.Snoozed)
	key := m.inboxKeyOr(config.ActionInboxShowSnoozed, "S")
	if n == 1 {
		return "1 snoozed. " + key + " shows it."
	}
	return strconv.Itoa(n) + " snoozed. " + key + " shows them."
}

// inboxSnoozedWhen is when a snoozed item wakes, in a few cells: "9:00",
// "tue 9:00", "changes".
func inboxSnoozedWhen(until int64, now time.Time) string {
	if until < 0 {
		return "until it changes"
	}
	at := time.Unix(0, until).In(now.Location())
	y, mo, d := now.Date()
	ay, amo, ad := at.Date()
	if y == ay && mo == amo && d == ad {
		return "until " + at.Format("15:04")
	}
	return "until " + at.Format("Mon 15:04")
}

// JumpToNewestFinished goes to the newest finished turn nobody has seen, and
// on a repeat within 5 seconds to the next older one (ctrl+b O). A turn that
// finishes during a walk starts it over at the newest. handled is false until
// an agent has been seen, so for a person who runs none the key after the
// prefix reaches the pane as it always did.
func (m *OS) JumpToNewestFinished() (tea.Cmd, bool) {
	if !m.agentsSeen() {
		return nil, false
	}
	st := &m.Inbox
	var done []session.AttentionItem
	for _, it := range st.Items {
		if it.Kind == session.AttentionFinished && !it.Stale {
			done = append(done, it)
		}
	}
	if len(done) == 0 {
		msg := "No finished turns you have not seen"
		if !st.Live {
			msg = "The Inbox is not connected to the daemon"
		}
		m.ShowNotification(msg, "info", m.Settings.NotificationDuration)
		return nil, true
	}
	// Newest first: an item's seq is the revision of its last change, which
	// for a finished turn is the turn finishing.
	slices.SortFunc(done, func(a, b session.AttentionItem) int { return cmp.Compare(b.Seq, a.Seq) })
	walk := &st.life.walk
	next := 0
	if walk.at.IsZero() || time.Since(walk.at) >= inboxCycleWindow || done[0].Seq > walk.newest {
		walk.newest = done[0].Seq
	} else {
		for i, it := range done {
			if it.Seq < walk.lastSeq {
				next = i
				break
			}
		}
	}
	it := done[next]
	walk.lastSeq, walk.at = it.Seq, time.Now()
	m.inboxJump(it)
	if len(done) > 1 {
		m.ShowNotification(strconv.Itoa(next+1)+" of "+strconv.Itoa(len(done))+" unseen: "+inboxWho(it)+" "+inboxKindWords(it), "info", m.Settings.NotificationDuration)
	}
	return nil, true
}

// railPane is what the rail knows of a pane of this machine: its agent
// state, its finished turns and its label. ok is false for a pane this client
// cannot see, such as one on another machine.
func (m *OS) railPane(sessionID, windowID string) (state string, seq uint64, label string, ok bool) {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w.AgentState, w.AgentCompletionSeq, m.railTitleShown(w), true
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return "", 0, "", false
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		if w.ID == windowID {
			return w.AgentState, w.CompletionSeq, railWindowLabel("", w.ForegroundCmd, w.Title), true
		}
	}
	return "", 0, "", false
}

// SidebarAgentUnread marks a rail agent row's finished turn unread again: this
// client forgets it looked, so the row reads as finished, and the daemon opens
// the pane's Finished item marked unread, so every client's Inbox has it. The
// pane in front of the person cannot be: focusing it is what marks it seen.
func (m *OS) SidebarAgentUnread(sessionID, windowID string) (tea.Cmd, bool) {
	state, seq, label, ok := m.railPane(sessionID, windowID)
	if !ok {
		m.ShowNotification("That pane is on another machine; attach there to mark it unread", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if seq == 0 && state != "done" {
		m.ShowNotification(printableTitle(label)+" has not finished a turn yet", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if state != "done" && state != "idle" && state != "unknown" {
		m.ShowNotification(printableTitle(label)+" is "+sidebarStateWords(state)+"; a turn it finishes shows up by itself", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if w := m.GetFocusedWindow(); w != nil && w.ID == windowID && (sessionID == "" || sessionID == m.sidebarCurrentSessionID()) {
		m.ShowNotification("This pane is in front of you, so it reads as seen. Mark it from another pane.", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	changed := false
	if m.SidebarAgentSeen[windowID] {
		delete(m.SidebarAgentSeen, windowID)
		changed = true
	}
	if _, had := m.SidebarAgentSeenSeq[windowID]; had {
		delete(m.SidebarAgentSeenSeq, windowID)
		changed = true
	}
	if changed {
		m.saveSidebarState()
		m.sidebarCache.invalidate()
	}
	who := printableTitle(label)
	// The daemon is asked whenever there is one: it holds the pane's turn
	// count, which this client's copy of another session can trail.
	// A daemon without mark-attention has no unread to set; the marks this
	// client cleared are the whole of it there.
	if !m.IsDaemonSession || !m.inboxMarkSupported() || (m.Inbox.call == nil && (m.DaemonClient == nil || m.AttachedHost != "")) {
		m.ShowNotification("Marked "+who+" unread", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	return m.inboxMarkCmd(map[string]any{"session": sessionID, "window": windowID, "action": "unread"}, who, ""), true
}

// SidebarAgentSnooze snoozes the Inbox item of a rail agent row's pane: the
// Inbox opens on the item with the four lengths in its footer, and closes
// again once one is picked.
func (m *OS) SidebarAgentSnooze(sessionID, windowID string) (tea.Cmd, bool) {
	if !m.inboxMarkSupported() {
		return nil, false
	}
	var found *session.AttentionItem
	for i := range m.Inbox.Items {
		it := &m.Inbox.Items[i]
		if it.Host != "" || it.Window != windowID || (sessionID != "" && it.Session != sessionID) {
			continue
		}
		if found == nil || session.AttentionKindRank(it.Kind) < session.AttentionKindRank(found.Kind) {
			found = it
		}
	}
	if found == nil {
		m.ShowNotification("Nothing in the Inbox for this pane to snooze", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if why := inboxSnoozeRefusal(*found); why != "" {
		m.ShowNotification(why, "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if !m.inboxCanMark("Snoozing") {
		return nil, true
	}
	id := found.ID
	m.OpenInbox("")
	m.Inbox.SelectedID = id
	m.clampInboxSelection()
	m.Inbox.life.pickFor = id
	m.Inbox.life.closeAfterPick = true
	return nil, true
}
