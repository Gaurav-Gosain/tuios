package app

import (
	"cmp"
	"slices"
	"sort"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

func (m *OS) inboxUndoWindow() time.Duration {
	return time.Duration(cmp.Or(max(m.Settings.UndoWindowSeconds, 0), config.DefaultUndoWindowSeconds)) * time.Second
}

const inboxUndoNotice = 5 * time.Second

func (m *OS) showUndoOffer(message string) {
	m.showNotification(message, "info", "", inboxUndoNotice, nil, m.inboxUndoWindow(), false)
}

const inboxUndoMax = 16

type inboxSnoozeChoice struct {
	key    string
	label  string
	words  string
	params func(now time.Time) map[string]any
}

var inboxSnoozeChoices = []inboxSnoozeChoice{
	{"1", "15m", "for 15 minutes", func(time.Time) map[string]any { return map[string]any{"for_ms": (15 * time.Minute).Milliseconds()} }},
	{"2", "1h", "for an hour", func(time.Time) map[string]any { return map[string]any{"for_ms": time.Hour.Milliseconds()} }},
	{"3", "tomorrow 9:00", "until 9:00 tomorrow", func(now time.Time) map[string]any {
		return map[string]any{"until": nextMorning(now).UnixMilli()}
	}},
	{"4", "until it changes", "until it changes", func(time.Time) map[string]any { return map[string]any{"until_change": true} }},
}

func nextMorning(now time.Time) time.Time {
	y, mo, d := now.Date()
	return time.Date(y, mo, d+1, 9, 0, 0, 0, now.Location())
}

type inboxLifecycle struct {
	Snoozed        []session.AttentionItem
	ShowSnoozed    bool
	pickFor        string
	closeAfterPick bool
	undo           []inboxUndoEntry
	walk           inboxFinishedWalk
	asked          map[string]time.Time
}

func probeVerb(call func(verb string, params map[string]any) ([]byte, error), name string) (supported, known bool) {
	raw, err := call("list-verbs", map[string]any{"verb": name})
	if err != nil {
		if code := session.VerbErrCode(err); code == session.ErrVerbUnknownVerb || code == session.ErrVerbInvalidParams {
			return false, true
		}
		return false, false
	}
	var res struct {
		Verbs []struct {
			Verb string `json:"verb"`
		} `json:"verbs"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return false, false
	}
	for _, v := range res.Verbs {
		if v.Verb == name {
			return true, true
		}
	}
	return false, true
}

const inboxAskedWindow = 10 * time.Second

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

func inboxUnreadKey(windowID string) string { return "unread:" + windowID }

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

type inboxUndoEntry struct {
	id                    string
	who                   string
	at                    time.Time
	session, window, item string
}

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
	// Window is the pane an unread or a settle was for, and Session its
	// session.
	Window, Session string
	// Words say what was done, for the dock: "for an hour".
	Words string
	Err   error
}

func (m *OS) inboxSnoozedIndex(id string) int {
	for i := range m.Inbox.life.Snoozed {
		if m.Inbox.life.Snoozed[i].ID == id {
			return i
		}
	}
	return -1
}

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

func (m *OS) inboxCanMark(what string) bool {
	return m.inboxCanAct(what)
}

func (m *OS) inboxMarkCmd(params map[string]any, who, words string) tea.Cmd {
	nonce := m.inboxNonce()
	action, _ := params["action"].(string)
	if nonce == "" {
		m.ShowNotification(m.noNonceText(), "error", m.Settings.NotificationDuration*2)
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
	case "unsettle":
		if id, _ := params["id"].(string); id != "" {
			m.noteInboxAsked(id)
		}
	}
	window, _ := params["window"].(string)
	sessionName, _ := params["session"].(string)
	call := m.inboxActCaller()
	return func() tea.Msg {
		raw, err := call("mark-attention", params, 5*time.Second)
		msg := InboxMarkedMsg{Action: action, Who: who, Words: words, Window: window, Session: sessionName, Err: err}
		if err == nil {
			msg.ID = markedID(raw)
		}
		return msg
	}
}

func (m *OS) applyInboxMarked(msg InboxMarkedMsg) {
	if msg.Err != nil {
		m.ShowNotification(inboxMarkFailWords(msg.Action)+": "+inboxActErr(msg.Err, m.AttachedHost), "error", m.Settings.NotificationDuration*2)
		if msg.Action == "unread" {
			m.settleInboxUnreadAsk(msg.Window, "")
		}
		return
	}
	switch msg.Action {
	case "snooze":
		m.rememberInboxUndo(msg.ID, msg.Who)
		m.showUndoOffer("Snoozed " + msg.Who + " " + msg.Words + ". " + m.inboxKeyOr(config.ActionInboxUndo, "u") + " in the agents view undoes.")
	case "wake":
		m.ShowNotification(capitalize(msg.Who)+" needs you again", "info", m.Settings.NotificationDuration)
	case "restore":
		m.ShowNotification("Restored "+msg.Who, "success", m.Settings.NotificationDuration)
	case "unread":
		m.settleInboxUnreadAsk(msg.Window, msg.ID)
		m.ShowNotification("Marked "+msg.Who+" unread", "info", m.Settings.NotificationDuration)
	case "settle":
		m.noteSettled(msg)
	case "unsettle":
		m.ShowNotification(capitalize(msg.Who)+" is off the Settled shelf", "info", m.Settings.NotificationDuration)
	}
}

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
	case "settle":
		return "The agent was not settled"
	case "unsettle":
		return "The agent is still settled"
	}
	return "Nothing changed"
}

func markedID(raw []byte) string {
	var res struct {
		ID string `json:"id"`
	}
	_ = jsonx.Unmarshal(raw, &res)
	return res.ID
}

func (m *OS) rememberInboxUndo(id, who string) {
	if id == "" {
		return
	}
	life := &m.Inbox.life
	now := time.Now()
	life.undo = slices.DeleteFunc(life.undo, func(u inboxUndoEntry) bool {
		return u.id == id || now.Sub(u.at) >= m.inboxUndoWindow()
	})
	life.undo = append(life.undo, inboxUndoEntry{id: id, who: who, at: now})
	if len(life.undo) > inboxUndoMax {
		life.undo = life.undo[len(life.undo)-inboxUndoMax:]
	}
}

func (m *OS) noteInboxDismissed(id, kind, who string) {
	if kind == session.AttentionOutbox || kind == session.AttentionAsk {
		return
	}
	m.rememberInboxUndo(id, who)
	m.showUndoOffer("Dismissed " + who + ". " + m.inboxKeyOr(config.ActionInboxUndo, "u") + " undoes.")
}

// InboxSnooze starts a snooze of the selected item: the footer offers the
// four lengths and the next digit picks one. On a snoozed item it wakes it.
func (m *OS) InboxSnooze() (tea.Cmd, bool) {
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

func inboxSnoozeRefusal(it session.AttentionItem) string {
	switch {
	case it.Kind == session.AttentionApproval && it.RequestID != "":
		return "tuios holds this approval for your answer, so it is not snoozed. Answer it or dismiss it."
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
		if id == bulkSnoozeID {
			now := time.Now()
			return m.bulkSnoozeCmd(func() map[string]any { return c.params(now) }, c.words)
		}
		it, ok := m.inboxItem(id)
		if !ok {
			m.ShowNotification("That item is gone", "warning", m.Settings.NotificationDuration)
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

func inboxSnoozeHints() []overlay.Hint {
	hints := make([]overlay.Hint, 0, len(inboxSnoozeChoices)+1)
	for _, c := range inboxSnoozeChoices {
		hints = append(hints, overlay.Hint{Key: c.key, Label: c.label})
	}
	return append(hints, overlay.Hint{Key: "esc", Label: "cancel"})
}

// InboxUndo reopens the item dismissed or snoozed last, within the undo window.
// Pressed again it reopens the one before.
func (m *OS) InboxUndo() (tea.Cmd, bool) {
	life := &m.Inbox.life
	now := time.Now()
	life.undo = slices.DeleteFunc(life.undo, func(u inboxUndoEntry) bool { return now.Sub(u.at) >= m.inboxUndoWindow() })
	if len(life.undo) == 0 {
		m.ShowNotification("Nothing to undo in the last "+units.Duration(m.inboxUndoWindow())+".", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	if !m.inboxCanMark("Undoing") {
		return nil, true
	}
	u := life.undo[len(life.undo)-1]
	life.undo = life.undo[:len(life.undo)-1]
	if u.window != "" {
		return m.settleCmd(sidebarAgentEntry{SessionID: u.session, WindowID: u.window, Title: u.who}, "unsettle", u.item), true
	}
	return m.inboxMarkCmd(map[string]any{"id": u.id, "action": "restore"}, u.who, ""), true
}

// InboxToggleSnoozed shows or hides the snoozed items under the list.
func (m *OS) InboxToggleSnoozed() (tea.Cmd, bool) {
	life := &m.Inbox.life
	if !life.ShowSnoozed && len(life.Snoozed) == 0 {
		m.ShowNotification("Nothing is snoozed", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	life.ShowSnoozed = !life.ShowSnoozed
	m.clampInboxSelection()
	return nil, true
}

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

func (m *OS) inboxSnoozedNote() string {
	n := len(m.Inbox.life.Snoozed)
	key := m.inboxKeyOr(config.ActionInboxShowSnoozed, "S")
	if n == 1 {
		return "1 snoozed. " + key + " shows it."
	}
	return strconv.Itoa(n) + " snoozed. " + key + " shows them."
}

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
	if !m.agentsOn() {
		return nil, false
	}
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
			msg = "The agents view is not connected to the daemon"
		}
		m.ShowNotification(msg, "info", m.Settings.NotificationDuration)
		return nil, true
	}
	sort.Slice(done, func(i, j int) bool { return done[i].Seq > done[j].Seq })
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
			return w.AgentState, w.CompletionSeq, railWindowLabel("", w.AgentHarness, w.ForegroundCmd, w.Title), true
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
		m.ShowNotification("That pane is on another machine; attach there to mark it unread", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	if seq == 0 && state != "done" {
		m.ShowNotification(printableTitle(label)+" has not finished a turn yet", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	if state != "done" && state != "idle" && state != "unknown" {
		m.ShowNotification(printableTitle(label)+" is "+sidebarStateWords(state)+"; a turn it finishes shows up by itself", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if w := m.GetFocusedWindow(); w != nil && w.ID == windowID && (sessionID == "" || sessionID == m.sidebarCurrentSessionID()) {
		m.ShowNotification("This pane is in front of you, so it reads as seen. Mark it from another pane.", "warning", m.Settings.NotificationDuration)
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
	if !m.IsDaemonSession || (m.Inbox.call == nil && m.DaemonClient == nil) {
		m.ShowNotification("Marked "+who+" unread", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	return m.inboxMarkCmd(map[string]any{"session": sessionID, "window": windowID, "action": "unread"}, who, ""), true
}

// SidebarAgentSnooze snoozes the Inbox item of a rail agent row's pane: the
// Inbox opens on the item with the four lengths in its footer, and closes
// again once one is picked.
func (m *OS) SidebarAgentSnooze(sessionID, windowID string) (tea.Cmd, bool) {
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
		m.ShowNotification("Nothing needs you in this pane, so there is nothing to snooze", "warning", m.Settings.NotificationDuration)
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
