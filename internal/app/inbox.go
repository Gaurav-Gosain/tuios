package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/sound"
)

// The Inbox on the client: a mirror of the daemon's attention queue (see
// session/attention.go), the overlay that reads it, and the alerts it raises.
//
// The mirror is fed by one watcher goroutine per client. It lists the queue,
// then subscribes to attention events from the position the listing was
// current to, so nothing is missed between the two and nothing is counted
// twice. A gap, a daemon restart or a dropped connection starts it over with a
// fresh listing. An idle Inbox costs one blocked read.
//
// Alerts for the attached session still come from the state sync, as they
// always have (agent_alert.go). The Inbox adds the rest: an agent in any other
// session that blocks, errors, finishes or writes to the person raises the
// same dock message, desktop notification and sound, naming the session. A
// burst, which is what a fan of agents produces, is one alert.

// inboxQueue bounds the channel from the watcher to Update.
const inboxQueue = 16

// inboxBatch is how long the watcher gathers events after the first one of a
// burst before handing them to Update together, so sixteen agents finishing
// in the same breath are one render and one alert.
const inboxBatch = 150 * time.Millisecond

// inboxRetryMax caps the wait between attempts to reach the daemon.
const inboxRetryMax = 30 * time.Second

// inboxCycleWindow is how long after a jump the next-attention key continues
// from the item it jumped to rather than starting at the oldest again.
const inboxCycleWindow = 5 * time.Second

// InboxState is the mirror and the overlay's state.
type InboxState struct {
	// Items is the open queue in Inbox order (session.SortAttention).
	Items []session.AttentionItem
	// Live is true while the watcher holds a current listing. The rail only
	// trusts the counts while it is.
	Live bool
	// Unsupported is true when the daemon is too old to have an Inbox.
	Unsupported bool
	// Gen counts changes, for the rail's render cache.
	Gen uint64
	// Filter narrows the overlay to one kind, empty for every kind.
	Filter string
	// Selected is the overlay's cursor, an index into its rows. It is always
	// on an item row, never on a group heading, unless there are no items.
	Selected int
	// SelectedID is the item under the cursor. The list re-sorts on every
	// event, so the cursor follows this id rather than staying on a row: a
	// key acts on the item the person selected, not on whatever moved into
	// its row since.
	SelectedID string
	// Scroll is the overlay's scroll offset.
	Scroll int
	// shown is the held approval the overlay last drew under the cursor, and
	// when it was first drawn as it is. 1, 2 and 3 answer only that item as
	// drawn, once it has been on screen for inboxAnswerSettle.
	shown inboxShown

	// lastJumpID and lastJumpAt let the next-attention key cycle.
	lastJumpID string
	lastJumpAt time.Time
	// pendingThread is a mail thread to open once the session it is in has
	// been switched to and its mail has loaded, and pendingReply says to open
	// its reply line too.
	pendingThread uint64
	pendingReply  bool

	// Peek is the prompt read over the list, nil when the list shows. See
	// inbox_peek.go.
	Peek *inboxPeek
	// peekGen numbers peeks, so a reply to one since closed is dropped.
	peekGen uint64
	// call and nonce replace the daemon and the attach nonce in tests.
	call  inboxVerbCall
	nonce func() string
	// replied holds the approval requests this client answered, so the close
	// event for one is not announced as answered elsewhere.
	replied map[string]bool
}

// InboxSnapshotMsg is a fresh listing from the watcher.
type InboxSnapshotMsg struct {
	Items []session.AttentionItem
}

// InboxEventsMsg is a batch of attention events from the watcher.
type InboxEventsMsg struct {
	Events []InboxEvent
}

// InboxEvent is one attention event as the stream carries it.
type InboxEvent struct {
	Action string                 `json:"action"`
	Item   *session.AttentionItem `json:"attention"`
}

// InboxDownMsg says the watcher lost the daemon. Unsupported says the daemon
// answered and has no Inbox, and the watcher has stopped for good.
type InboxDownMsg struct {
	Err         error
	Unsupported bool
}

// InboxAlertDueMsg is the settle window of a set of alerts running out.
type InboxAlertDueMsg struct {
	IDs []string
}

// InboxDismissedMsg is the answer to a dismiss.
type InboxDismissedMsg struct {
	Err error
	// Silent marks a dismiss the client sent on its own, such as a finished
	// turn seen under the person's eyes. Its failure is never shown: the
	// person did not ask for it, and the next event or listing corrects the
	// mirror either way.
	Silent bool
}

// inboxWatchMsg wraps what the watcher delivers, so Update can re-arm the
// listener for exactly these messages.
type inboxWatchMsg struct {
	msg tea.Msg
}

// errInboxGap ends one watch so the next one starts from a fresh listing.
var errInboxGap = errors.New("the attention stream has a gap")

// inboxDial opens a verb connection to the daemon that holds the queue.
type inboxDial func() (*session.VerbClient, error)

// watchesInbox reports whether this model follows the daemon's queue: a real
// client of a daemon session. Tests build models with no kind and get none.
func (m *OS) watchesInbox() bool {
	return m.Client != ClientUnknown && !m.ScriptMode && m.IsDaemonSession && m.DaemonClient != nil
}

// startInboxWatch starts the watcher and returns the command that waits for
// its first delivery. The queue is this machine's daemon's, whichever session
// the client is attached to.
func (m *OS) startInboxWatch() tea.Cmd {
	if !m.watchesInbox() || m.inboxEvents != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, inboxQueue)
	m.inboxEvents, m.stopInbox = ch, cancel
	build := m.DaemonClient.ClientVersion()
	go runInboxWatch(ctx, func() (*session.VerbClient, error) { return session.DialVerbClientAs(build) }, ch)
	return listenForInbox(ch)
}

// endInboxWatch stops the watcher.
func (m *OS) endInboxWatch() {
	if m.stopInbox != nil {
		m.stopInbox()
		m.stopInbox = nil
	}
}

// listenForInbox waits for one delivery from the watcher.
func listenForInbox(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return inboxWatchMsg{msg: msg}
	}
}

// runInboxWatch keeps a watch running until ctx ends, starting over after
// every failure with a growing wait.
func runInboxWatch(ctx context.Context, dial inboxDial, out chan<- tea.Msg) {
	defer close(out)
	wait := time.Second
	for ctx.Err() == nil {
		listed, err := inboxWatchOnce(ctx, dial, out)
		if ctx.Err() != nil {
			return
		}
		var callErr *session.VerbCallError
		if errors.As(err, &callErr) && callErr.Code == session.ErrVerbUnknownVerb {
			inboxSend(ctx, out, InboxDownMsg{Err: err, Unsupported: true})
			return
		}
		inboxSend(ctx, out, InboxDownMsg{Err: err})
		if listed {
			wait = time.Second
		}
		if errors.Is(err, errInboxGap) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, inboxRetryMax)
	}
}

// inboxSend delivers one message unless ctx ends first.
func inboxSend(ctx context.Context, out chan<- tea.Msg, msg tea.Msg) bool {
	select {
	case out <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// inboxWatchOnce lists the queue, subscribes from the listing's position and
// delivers events until the connection fails or the stream has a gap. It
// reports whether the listing was delivered.
func inboxWatchOnce(ctx context.Context, dial inboxDial, out chan<- tea.Msg) (bool, error) {
	client, err := dial()
	if err != nil {
		return false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	defer func() { _ = client.Close() }()

	raw, err := client.CallWithTimeout("list-attention", map[string]any{}, 5*time.Second)
	if err != nil {
		return false, err
	}
	var listing struct {
		Items  []session.AttentionItem `json:"items"`
		Seq    uint64                  `json:"seq"`
		BootID string                  `json:"boot_id"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return false, err
	}
	if _, err := client.CallWithTimeout("subscribe", map[string]any{
		"types":     []string{session.EventAttention},
		"after_seq": listing.Seq,
		"boot_id":   listing.BootID,
	}, 5*time.Second); err != nil {
		return false, err
	}
	if !inboxSend(ctx, out, InboxSnapshotMsg{Items: listing.Items}) {
		return true, ctx.Err()
	}

	// One goroutine reads, this one batches: a read deadline cannot be used to
	// end a batch, because a line cut by a deadline is lost.
	type read struct {
		ev  InboxEvent
		gap bool
		err error
	}
	lines := make(chan read, 64)
	// quit ends the reader when this watch returns for any reason, so a reader
	// holding a line nobody will take does not outlive it.
	quit := make(chan struct{})
	defer close(quit)
	put := func(r read) bool {
		select {
		case lines <- r:
			return true
		case <-quit:
			return false
		}
	}
	go func() {
		defer close(lines)
		for {
			line, err := client.ReadEventLine(0)
			if err != nil {
				put(read{err: err})
				return
			}
			var ev struct {
				Type string `json:"type"`
				InboxEvent
			}
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			switch {
			case ev.Type == session.EventGap:
				put(read{gap: true})
				return
			case ev.Type == session.EventAttention && ev.Item != nil:
				if !put(read{ev: ev.InboxEvent}) {
					return
				}
			}
		}
	}()

	for {
		first, ok := <-lines
		if !ok {
			return true, errors.New("the attention stream ended")
		}
		if first.err != nil {
			return true, first.err
		}
		if first.gap {
			return true, errInboxGap
		}
		batch := []InboxEvent{first.ev}
		timer := time.NewTimer(inboxBatch)
		var end error
	gather:
		for {
			select {
			case r, ok := <-lines:
				switch {
				case !ok:
					end = errors.New("the attention stream ended")
					break gather
				case r.err != nil:
					end = r.err
					break gather
				case r.gap:
					end = errInboxGap
					break gather
				}
				batch = append(batch, r.ev)
			case <-timer.C:
				break gather
			}
		}
		timer.Stop()
		if !inboxSend(ctx, out, InboxEventsMsg{Events: batch}) {
			return true, ctx.Err()
		}
		if end != nil {
			return true, end
		}
	}
}

// handleInboxWatch applies one delivery and re-arms the listener.
func (m *OS) handleInboxWatch(msg inboxWatchMsg) tea.Cmd {
	var cmd tea.Cmd
	switch inner := msg.msg.(type) {
	case InboxSnapshotMsg:
		m.applyInboxSnapshot(inner)
	case InboxEventsMsg:
		cmd = m.applyInboxEvents(inner)
	case InboxDownMsg:
		m.applyInboxDown(inner)
		if inner.Unsupported {
			return nil
		}
	case nil:
		return nil
	}
	return tea.Batch(cmd, listenForInbox(m.inboxEvents))
}

// applyInboxSnapshot replaces the mirror with a fresh listing.
func (m *OS) applyInboxSnapshot(msg InboxSnapshotMsg) {
	st := &m.Inbox
	st.Items = append(st.Items[:0], msg.Items...)
	session.SortAttention(st.Items)
	st.Live = true
	st.Unsupported = false
	m.inboxChanged()
}

// applyInboxDown marks the mirror stale. The items are kept for the overlay,
// which says the list may be out of date, and the rail stops counting from it.
func (m *OS) applyInboxDown(msg InboxDownMsg) {
	st := &m.Inbox
	st.Live = false
	st.Unsupported = msg.Unsupported
	m.inboxChanged()
}

// applyInboxEvents folds a batch of events into the mirror and returns the
// command that raises its alerts once they have settled.
func (m *OS) applyInboxEvents(msg InboxEventsMsg) tea.Cmd {
	st := &m.Inbox
	var alert []string
	var cmds []tea.Cmd
	for _, ev := range msg.Events {
		it := ev.Item
		if it == nil {
			continue
		}
		idx := m.inboxIndex(it.ID)
		switch ev.Action {
		case session.AttentionClosed:
			if idx >= 0 {
				m.noteAnsweredElsewhere(st.Items[idx], *it)
				st.Items = append(st.Items[:idx], st.Items[idx+1:]...)
			}
		case session.AttentionOpened, session.AttentionUpdated:
			more := false
			if idx >= 0 {
				more = it.Kind == session.AttentionMail && it.Count > st.Items[idx].Count
				st.Items[idx] = *it
			} else {
				st.Items = append(st.Items, *it)
			}
			if ev.Action == session.AttentionOpened || more {
				alert = append(alert, it.ID)
			}
			cmds = append(cmds, m.inboxSeenUnderEyes(*it))
		}
	}
	session.SortAttention(st.Items)
	m.inboxChanged()
	return tea.Batch(append(cmds, m.scheduleInboxAlerts(alert))...)
}

// inboxIndex finds an item in the mirror by id, or -1.
func (m *OS) inboxIndex(id string) int {
	for i := range m.Inbox.Items {
		if m.Inbox.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// inboxItem returns an item in the mirror by id.
func (m *OS) inboxItem(id string) (session.AttentionItem, bool) {
	if i := m.inboxIndex(id); i >= 0 {
		return m.Inbox.Items[i], true
	}
	return session.AttentionItem{}, false
}

// inboxChanged records that the mirror moved, so the rail and the overlay
// redraw.
func (m *OS) inboxChanged() {
	m.Inbox.Gen++
	m.clampInboxSelection()
	m.sidebarCache.invalidate()
}

// inboxAttached reports whether an item is in the session this client is
// attached to, whose transitions the state sync already alerts on.
func (m *OS) inboxAttached(it session.AttentionItem) bool {
	return it.Host == "" && m.AttachedHost == "" && m.IsDaemonSession && it.Session == m.sidebarCurrentSessionID()
}

// inboxSeenUnderEyes dismisses a finished item for the pane this client is
// showing: a turn that finished under the person's eyes has been seen, which is
// the rule the rail applies to its own unread mark.
func (m *OS) inboxSeenUnderEyes(it session.AttentionItem) tea.Cmd {
	if it.Kind != session.AttentionFinished || !m.inboxAttached(it) {
		return nil
	}
	if w := m.GetFocusedWindow(); w != nil && w.ID == it.Window {
		return m.inboxDismissCmd(it.ID, true)
	}
	return nil
}

// inboxAlertState is the agent state whose alert policy governs a kind.
func inboxAlertState(kind string) string {
	switch kind {
	case session.AttentionApproval, session.AttentionQuestion:
		return "needs_input"
	case session.AttentionErrored:
		return "errored"
	case session.AttentionFinished:
		return "done"
	}
	return ""
}

// inboxAlertable reports whether an item earns an alert from the Inbox: it is
// not in the attached session, whose alerts come from the state sync, and the
// policy alerts on its kind.
func (m *OS) inboxAlertable(it session.AttentionItem, policy config.AgentAlertPolicy) bool {
	if m.inboxAttached(it) {
		return false
	}
	if it.Kind == session.AttentionMail {
		return policy.Enabled
	}
	return policy.Alerts(inboxAlertState(it.Kind))
}

// scheduleInboxAlerts parks the alertable items for the settle window. An item
// that closes inside it, a pane flickering through needs_input, raises nothing.
func (m *OS) scheduleInboxAlerts(ids []string) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	policy := m.agentAlertPolicy()
	var keep []string
	for _, id := range ids {
		it, ok := m.inboxItem(id)
		if ok && m.inboxAlertable(it, policy) {
			keep = append(keep, id)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	if policy.Settle <= 0 {
		m.fireInboxAlerts(keep)
		return nil
	}
	return tea.Tick(policy.Settle, func(time.Time) tea.Msg { return InboxAlertDueMsg{IDs: keep} })
}

// fireInboxAlerts raises one alert for the items still open, however many
// there are: the dock message names the session and the summary for one item,
// and counts for several.
func (m *OS) fireInboxAlerts(ids []string) {
	policy := m.agentAlertPolicy()
	if !policy.Enabled || policy.Quiet(time.Now()) {
		return
	}
	var items []session.AttentionItem
	for _, id := range ids {
		if it, ok := m.inboxItem(id); ok && !m.inboxAttached(it) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return
	}
	session.SortAttention(items)
	first := items[0]
	text := inboxAlertText(items)
	sev := "info"
	cue, cueState := sound.CueDone, "done"
	for _, it := range items {
		switch it.Kind {
		case session.AttentionApproval, session.AttentionQuestion, session.AttentionMail:
			sev, cue, cueState = "warning", sound.CueAttention, "needs_input"
		case session.AttentionErrored:
			if sev != "warning" {
				sev = "error"
			}
			cue, cueState = sound.CueAttention, "errored"
		case session.AttentionFinished:
			if sev == "info" {
				sev = "success"
			}
		}
	}
	if policy.Dock {
		m.ShowNotificationFrom(text, sev, m.Settings.NotificationDuration,
			NotifTarget{SessionID: first.Session, WindowID: first.Window})
	}
	var seq []byte
	if policy.Notify && !m.BrowserClient {
		seq = hostNotifySequence(text, m.detectOuterMultiplexer())
	}
	if policy.PlaysBell() {
		seq = append(seq, 0x07)
	}
	m.writeHostSequence(seq)
	if policy.PlaysAudio() {
		sound.Play(sound.Request{Cue: cue, File: policy.CueFile(cueState), Cooldown: policy.SoundCooldown})
	}
}

// applyInboxAlertDue raises the alerts whose settle window ran out, for the
// items still open.
func (m *OS) applyInboxAlertDue(msg InboxAlertDueMsg) {
	m.fireInboxAlerts(msg.IDs)
}

// inboxKindWords is how an alert says what an item is.
func inboxKindWords(it session.AttentionItem) string {
	switch it.Kind {
	case session.AttentionApproval:
		return "needs approval"
	case session.AttentionQuestion:
		return "has a question"
	case session.AttentionErrored:
		return "errored"
	case session.AttentionFinished:
		return "finished"
	case session.AttentionMail:
		return "wrote to you"
	}
	return it.Kind
}

// inboxWho is what an item is about: the pane's name, else a short id.
func inboxWho(it session.AttentionItem) string {
	if name := printableTitle(it.Name); name != "" {
		return name
	}
	if it.Window != "" {
		return shortWindowLabel(it.Window)
	}
	return "an agent"
}

// inboxWhere is the session an item is in, with its machine when it is not
// this one.
func inboxWhere(it session.AttentionItem) string {
	where := printableTitle(it.Session)
	if it.Host != "" {
		where = printableTitle(it.Host) + ":" + where
	}
	return where
}

// inboxAlertText is the dock line for a burst: one item in full, naming its
// session, or a count and the sessions for several.
func inboxAlertText(items []session.AttentionItem) string {
	if len(items) == 1 {
		it := items[0]
		text := inboxWhere(it) + ": " + inboxWho(it) + " " + inboxKindWords(it)
		if note := printableTitle(it.Summary); note != "" {
			text += agentAlertSep() + note
		}
		return text
	}
	var sessions []string
	seen := map[string]bool{}
	for _, it := range items {
		where := inboxWhere(it)
		if !seen[where] {
			seen[where] = true
			sessions = append(sessions, where)
		}
	}
	names := strings.Join(sessions, ", ")
	if len(sessions) > 3 {
		names = strings.Join(sessions[:3], ", ") + " and " + strconv.Itoa(len(sessions)-3) + " more"
	}
	return strconv.Itoa(len(items)) + " agents need you in " + names
}

// inboxCounts is the Inbox as the rail's agents header counts it: items that
// block an agent or report an error are blocked, finished items are done, and
// worst is the most urgent state among the blocked. session narrows it to one
// session, empty for all.
func (m *OS) inboxCounts(sessionName string) sidebarAgentCountInfo {
	var c sidebarAgentCountInfo
	rank := 0
	for _, it := range m.Inbox.Items {
		if it.Host != "" || (sessionName != "" && it.Session != sessionName) {
			continue
		}
		switch it.Kind {
		case session.AttentionApproval, session.AttentionQuestion, session.AttentionErrored:
			c.Blocked++
			state := inboxAlertState(it.Kind)
			if r := sessiontree.AgentRank(state, false); r > rank {
				c.Worst, rank = state, r
			}
		case session.AttentionFinished:
			c.Done++
		}
	}
	return c
}

// sidebarHeaderCounts is the agents header's readout. With a live Inbox it
// counts the Inbox, which sees every session on this machine whether or not the
// rail lists it, plus the rows of other machines, which the Inbox does not hold
// yet. Without one it counts the rows, as it always has.
func (m *OS) sidebarHeaderCounts(agents []sidebarAgentEntry) sidebarAgentCountInfo {
	if !m.Inbox.Live || m.AttachedHost != "" {
		return sidebarAgentCounts(agents)
	}
	here := ""
	if m.sidebarAgentsFilter() == sidebarAgentsSession {
		here = m.sidebarCurrentSessionID()
	}
	c := m.inboxCounts(here)
	var remote []sidebarAgentEntry
	for _, e := range agents {
		if e.Host != "" {
			remote = append(remote, e)
		}
	}
	if len(remote) > 0 {
		r := sidebarAgentCounts(remote)
		c.Blocked += r.Blocked
		c.Done += r.Done
		if sessiontree.AgentRank(r.Worst, false) > sessiontree.AgentRank(c.Worst, false) {
			c.Worst = r.Worst
		}
	}
	return c
}

// inboxRow is one line of the overlay: a group heading, or an item.
type inboxRow struct {
	heading string
	count   int
	item    *session.AttentionItem
}

// inboxGroupTitle is the heading a kind's rows sit under. It is words, so the
// grouping reads without colour.
func inboxGroupTitle(kind string) string {
	switch kind {
	case session.AttentionApproval:
		return "Approvals"
	case session.AttentionQuestion:
		return "Questions"
	case session.AttentionMail:
		return "Mail"
	case session.AttentionErrored:
		return "Errored"
	case session.AttentionFinished:
		return "Finished"
	}
	return kind
}

// inboxRows is the overlay's lines: each kind's heading and its items, oldest
// first, narrowed to the filter.
func (m *OS) inboxRows() []inboxRow {
	st := &m.Inbox
	var rows []inboxRow
	for i := 0; i < len(st.Items); {
		kind := st.Items[i].Kind
		j := i
		for j < len(st.Items) && st.Items[j].Kind == kind {
			j++
		}
		if st.Filter == "" || st.Filter == kind {
			rows = append(rows, inboxRow{heading: inboxGroupTitle(kind), count: j - i})
			for k := i; k < j; k++ {
				rows = append(rows, inboxRow{item: &st.Items[k]})
			}
		}
		i = j
	}
	return rows
}

// clampInboxSelection keeps the cursor on an item row: on the selected item's
// row wherever the list moved it, else the nearest item row to where it was.
func (m *OS) clampInboxSelection() {
	rows := m.inboxRows()
	st := &m.Inbox
	defer m.syncInboxSelectedID()
	if len(rows) == 0 {
		st.Selected = 0
		return
	}
	if st.SelectedID != "" {
		for i, r := range rows {
			if r.item != nil && r.item.ID == st.SelectedID {
				st.Selected = i
				return
			}
		}
	}
	st.Selected = clampInt(st.Selected, 0, len(rows)-1)
	if rows[st.Selected].item != nil {
		return
	}
	for i := st.Selected; i < len(rows); i++ {
		if rows[i].item != nil {
			st.Selected = i
			return
		}
	}
	for i := st.Selected; i >= 0; i-- {
		if rows[i].item != nil {
			st.Selected = i
			return
		}
	}
}

// syncInboxSelectedID records the item under the cursor as the one to follow.
func (m *OS) syncInboxSelectedID() {
	if it, ok := m.inboxSelected(); ok {
		m.Inbox.SelectedID = it.ID
		return
	}
	m.Inbox.SelectedID = ""
}

// OpenInbox shows the Inbox, narrowed to one kind or to none.
func (m *OS) OpenInbox(filter string) {
	st := &m.Inbox
	m.ShowInbox = true
	st.Filter = filter
	st.Selected, st.SelectedID = 0, ""
	st.Scroll = 0
	st.Peek = nil
	// Nothing has been on screen yet, so nothing can be answered until it is.
	st.shown = inboxShown{}
	m.clampInboxSelection()
}

// CloseInbox hides the Inbox, and the peek with it.
func (m *OS) CloseInbox() {
	m.ShowInbox = false
	m.Inbox.Peek = nil
}

// InboxMove moves the cursor by delta items, stepping over group headings.
func (m *OS) InboxMove(delta int) {
	rows := m.inboxRows()
	st := &m.Inbox
	if len(rows) == 0 || delta == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	i := st.Selected
	for delta > 0 {
		next := i + step
		for next >= 0 && next < len(rows) && rows[next].item == nil {
			next += step
		}
		if next < 0 || next >= len(rows) {
			break
		}
		i = next
		delta--
	}
	st.Selected = i
	m.syncInboxSelectedID()
}

// InboxSelect puts the cursor on row idx, when it is an item.
func (m *OS) InboxSelect(idx int) {
	rows := m.inboxRows()
	if idx >= 0 && idx < len(rows) && rows[idx].item != nil {
		m.Inbox.Selected = idx
		m.syncInboxSelectedID()
	}
}

// inboxSelected is the item under the cursor.
func (m *OS) inboxSelected() (session.AttentionItem, bool) {
	rows := m.inboxRows()
	i := m.Inbox.Selected
	if i < 0 || i >= len(rows) || rows[i].item == nil {
		return session.AttentionItem{}, false
	}
	return *rows[i].item, true
}

// InboxCycleFilter steps the filter through every kind and back to none.
func (m *OS) InboxCycleFilter() {
	st := &m.Inbox
	next := ""
	if st.Filter == "" {
		next = session.AttentionKindNames[0]
	} else if i := session.AttentionKindRank(st.Filter); i+1 < len(session.AttentionKindNames) {
		next = session.AttentionKindNames[i+1]
	}
	st.Filter = next
	st.Selected, st.SelectedID = 0, ""
	st.Scroll = 0
	m.clampInboxSelection()
}

// InboxActivate jumps to the selected item's pane, switching session and
// workspace, and closes the Inbox. Mail opens its thread instead, which is
// where the answer is.
func (m *OS) InboxActivate() tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	m.CloseInbox()
	if it.Kind == session.AttentionMail {
		return m.inboxOpenMail(it, false)
	}
	var cmd tea.Cmd
	if it.RequestID != "" {
		// A held prompt shows nothing in its pane. Going there is choosing to
		// answer it there, so the hold ends first and the harness shows it.
		cmd = m.inboxReplyCmd(it, session.ApprovalAsk)
	}
	m.inboxJump(it)
	return cmd
}

// InboxApprovalRepliedMsg is the answer to a reply-approval.
type InboxApprovalRepliedMsg struct {
	Name     string
	Decision string
	// Standing is the decision that stands, which is an earlier reply's
	// when Applied is false.
	Standing string
	Applied  bool
	// Reason is the daemon's reason. changed means the prompt was on another
	// line by the time the answer arrived, and nothing was answered.
	Reason string
	Err    error
}

// InboxReplyApproval answers the selected held approval with decision: once,
// always or deny. Anything that is not a held approval, or a decision its
// harness does not take, is refused here with a word, before anything is sent.
// The peek's InboxAnswer is the other way to answer: it presses the keys of a
// prompt the pane shows, where this answers one a hook holds off the screen.
func (m *OS) InboxReplyApproval(decision string) tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	if it.Kind != session.AttentionApproval || it.RequestID == "" {
		m.ShowNotification("1, 2 and 3 answer an approval the Inbox is holding. Enter goes to the pane.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if !slices.Contains(it.Options, decision) {
		m.ShowNotification("This prompt does not offer "+inboxDecisionWords(decision), "info", m.Settings.NotificationDuration)
		return nil
	}
	if !inboxShowsWhole(it) {
		m.ShowNotification("This prompt cannot be shown whole here. Enter answers it in the pane", "info", m.Settings.NotificationDuration)
		return nil
	}
	if !m.inboxAnswerSettled(it, time.Now()) {
		m.ShowNotification("This prompt just changed. Read it, then answer", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.inboxReplyCmd(it, decision)
}

// inboxAnswerSettle is how long a held approval must have been on screen, as
// it is, before a key answers it. An item that arrived, moved under the
// cursor or changed its text just before the key was pressed was not what
// the person read, so the key does nothing and they are told to look again.
const inboxAnswerSettle = 400 * time.Millisecond

// inboxShown is a held approval as the overlay drew it.
type inboxShown struct {
	key   string
	since time.Time
}

// inboxShownKey is everything about a held approval an answer depends on: the
// item, the hold, the line, the answers and what always adds.
func inboxShownKey(it session.AttentionItem) string {
	return strings.Join([]string{
		it.ID, it.RequestID, it.Summary,
		strings.Join(it.Options, ","), strings.Join(it.AlwaysScope, "\n"),
	}, "\x00")
}

// noteInboxShown records the held approval under the cursor as drawn at now,
// keeping the time it was first drawn while it stays the same.
func (m *OS) noteInboxShown(it session.AttentionItem, held bool, now time.Time) {
	st := &m.Inbox
	if !held {
		st.shown = inboxShown{}
		return
	}
	if key := inboxShownKey(it); st.shown.key != key {
		st.shown = inboxShown{key: key, since: now}
	}
}

// inboxAnswerSettled reports whether it is the held approval last drawn under
// the cursor, exactly as drawn, and has been on screen long enough to have
// been read.
func (m *OS) inboxAnswerSettled(it session.AttentionItem, now time.Time) bool {
	st := &m.Inbox
	return st.shown.key != "" && st.shown.key == inboxShownKey(it) && now.Sub(st.shown.since) >= inboxAnswerSettle
}

// inboxReplyCmd is the reply-approval call, with this client's attach nonce,
// which is what lets the daemon take the answer as the person's.
func (m *OS) inboxReplyCmd(it session.AttentionItem, decision string) tea.Cmd {
	if m.DaemonClient == nil || m.AttachedHost != "" {
		m.ShowNotification("Answering needs a client attached to this machine's daemon", "info", m.Settings.NotificationDuration)
		return nil
	}
	nonce := m.DaemonClient.HumanNonce()
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	if m.Inbox.replied == nil {
		m.Inbox.replied = make(map[string]bool)
	}
	m.Inbox.replied[it.RequestID] = true
	build := m.DaemonClient.ClientVersion()
	name := inboxWho(it)
	return func() tea.Msg {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return InboxApprovalRepliedMsg{Name: name, Decision: decision, Err: err}
		}
		defer func() { _ = client.Close() }()
		raw, err := client.CallWithTimeout("reply-approval", map[string]any{
			"request_id":  it.RequestID,
			"decision":    decision,
			"human_nonce": nonce,
			// The line the person read. The daemon answers nothing when
			// the hold is on another one.
			"summary": it.Summary,
		}, 5*time.Second)
		if err != nil {
			return InboxApprovalRepliedMsg{Name: name, Decision: decision, Err: err}
		}
		var res struct {
			Decision string `json:"decision"`
			Applied  bool   `json:"applied"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return InboxApprovalRepliedMsg{Name: name, Decision: decision, Err: err}
		}
		return InboxApprovalRepliedMsg{Name: name, Decision: decision, Standing: res.Decision, Applied: res.Applied, Reason: res.Reason}
	}
}

// applyInboxApprovalReplied says what became of an answer. A hand back from
// enter says nothing when it worked: the pane in front of the person is the
// answer.
func (m *OS) applyInboxApprovalReplied(msg InboxApprovalRepliedMsg) {
	switch {
	case msg.Err != nil:
		if msg.Decision == session.ApprovalAsk {
			return
		}
		m.ShowNotification("The answer did not go through: "+msg.Err.Error()+". Answer in the pane", "error", m.Settings.NotificationDuration*2)
	case !msg.Applied && msg.Reason == inboxReplyChanged:
		m.ShowNotification(msg.Name+" is asking about something else now, so nothing was answered. Read it again", "info", m.Settings.NotificationDuration*2)
	case !msg.Applied:
		m.ShowNotification(msg.Name+" was already answered: "+inboxDecisionWords(msg.Standing), "info", m.Settings.NotificationDuration)
	case msg.Decision != session.ApprovalAsk:
		m.ShowNotification(msg.Name+": "+inboxDecisionWords(msg.Decision), "success", m.Settings.NotificationDuration)
	}
}

// noteAnsweredElsewhere tells this client that an approval it was showing was
// answered from another client, so two people at two screens know.
func (m *OS) noteAnsweredElsewhere(was, closed session.AttentionItem) {
	if closed.Closed != session.AttentionClosedAnswered || was.RequestID == "" || m.Inbox.replied[was.RequestID] {
		return
	}
	m.ShowNotification(inboxWho(was)+" was answered from another client: "+inboxDecisionWords(closed.Answer), "info", m.Settings.NotificationDuration)
}

// inboxReplyChanged is reply-approval's reason for an answer refused because
// the held prompt is on another line than the one it was made from.
const inboxReplyChanged = "changed"

// inboxDecisionWords is a decision as the person reads it.
func inboxDecisionWords(decision string) string {
	switch decision {
	case session.ApprovalOnce:
		return "allowed once"
	case session.ApprovalAlways:
		return "always allowed"
	case session.ApprovalDeny:
		return "denied"
	case session.ApprovalAsk:
		return "handed back to the pane"
	}
	return decision
}

// inboxJump lands on an item's pane.
func (m *OS) inboxJump(it session.AttentionItem) {
	if it.Host != "" || m.AttachedHost != "" {
		m.ShowNotification("That pane is on another machine; attach there to reach it", "info", m.Settings.NotificationDuration)
		return
	}
	if it.Window == "" {
		m.ShowNotification("That item has no pane to go to", "info", m.Settings.NotificationDuration)
		return
	}
	// Not jumpToNotifTarget: that checks the session against the client's
	// cached listing, which trails a session created since, and the Inbox has
	// just been told by the daemon that the session exists. The switch itself
	// says so when it does not.
	idx := -1
	if it.Session == m.sidebarCurrentSessionID() {
		if idx = m.windowIndexByID(it.Window); idx < 0 {
			m.ShowNotification("Source pane closed", "info", m.Settings.NotificationDuration)
			return
		}
	}
	landed, ok := m.sidebarFocusWindow(sidebarRowHit{
		Kind:        sidebarRowWindow,
		SessionID:   it.Session,
		WindowID:    it.Window,
		WindowIndex: idx,
	})
	if !ok || landed < 0 || landed >= len(m.Windows) {
		if it.Session == m.sidebarCurrentSessionID() {
			m.ShowNotification("Source pane closed", "info", m.Settings.NotificationDuration)
		}
		return
	}
	// Flash the pane, as a jump from the dock does, so the eye follows.
	m.Windows[landed].MinimizeHighlightUntil = time.Now().Add(time.Second)
}

// InboxReply answers the selected mail: the thread opens with its reply line.
func (m *OS) InboxReply() tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	if it.Kind != session.AttentionMail {
		m.ShowNotification("r replies to mail. Enter goes to the pane.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.CloseInbox()
	return m.inboxOpenMail(it, true)
}

// inboxOpenMail opens a mail item's thread, in its session, with the reply
// line when reply is set. A thread in another session waits for that
// session's mail to load.
func (m *OS) inboxOpenMail(it session.AttentionItem, reply bool) tea.Cmd {
	if it.Host != "" || m.AttachedHost != "" {
		m.ShowNotification("That mail is on another machine; attach there to read it", "info", m.Settings.NotificationDuration)
		return nil
	}
	if it.Session != m.sidebarCurrentSessionID() {
		if err := m.SwitchToSession(it.Session); err != nil {
			m.ShowNotification("Switch failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
			return nil
		}
		m.Inbox.pendingThread, m.Inbox.pendingReply = it.Thread, reply
		return nil
	}
	return m.openMailThread(it.Thread, reply)
}

// openMailThread opens one thread and, when reply is set, its reply line.
func (m *OS) openMailThread(thread uint64, reply bool) tea.Cmd {
	cmd := m.OpenAgentMailThread(thread)
	if len(m.agentMailThreadMessages(thread)) == 0 {
		m.AgentMail.Error = "This thread is no longer in the ring. Dismiss it from the Inbox with d."
		return cmd
	}
	if reply {
		m.AgentMailStartReply()
	}
	return cmd
}

// takePendingInboxThread opens the thread an Inbox action was waiting to open,
// once the session's mail has loaded.
func (m *OS) takePendingInboxThread() tea.Cmd {
	thread, reply := m.Inbox.pendingThread, m.Inbox.pendingReply
	if thread == 0 {
		return nil
	}
	m.Inbox.pendingThread, m.Inbox.pendingReply = 0, false
	return m.openMailThread(thread, reply)
}

// InboxOpenMailbox opens the full mailbox, every thread including the ones
// between agents, from the Inbox.
func (m *OS) InboxOpenMailbox() tea.Cmd {
	m.CloseInbox()
	return m.OpenAgentMail()
}

// InboxDismiss dismisses the selected item on the daemon. The item leaves the
// list when the close event comes back, so every client agrees on when.
func (m *OS) InboxDismiss() tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	return m.inboxDismissCmd(it.ID, false)
}

// inboxDismissCmd is the dismiss-attention call, with this client's attach
// nonce, which is what lets the daemon tell the person from an agent. silent
// marks a dismiss the client sends on its own, which shows nothing when it
// cannot be sent or fails.
func (m *OS) inboxDismissCmd(id string, silent bool) tea.Cmd {
	if m.DaemonClient == nil || m.AttachedHost != "" {
		if !silent {
			m.ShowNotification("Dismissing needs a client attached to this machine's daemon", "info", m.Settings.NotificationDuration)
		}
		return nil
	}
	nonce := m.DaemonClient.HumanNonce()
	if nonce == "" {
		if !silent {
			m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		}
		return nil
	}
	build := m.DaemonClient.ClientVersion()
	return func() tea.Msg {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return InboxDismissedMsg{Err: err, Silent: silent}
		}
		defer func() { _ = client.Close() }()
		_, err = client.CallWithTimeout("dismiss-attention", map[string]any{"id": id, "human_nonce": nonce}, 5*time.Second)
		return InboxDismissedMsg{Err: err, Silent: silent}
	}
}

// inboxDismissGone reports whether a dismiss failed only because the item was
// no longer open: another client dismissed it, or the daemon closed it on its
// own. The item is gone either way, which is what the dismiss asked for.
func inboxDismissGone(err error) bool {
	var callErr *session.VerbCallError
	return errors.As(err, &callErr) && callErr.Code == session.ErrVerbInvalidParams &&
		callErr.Hint != nil && callErr.Hint.Param == "id"
}

// applyInboxDismissed says what went wrong, when something did. A dismiss the
// client sent on its own, and an item that was already closed, say nothing.
func (m *OS) applyInboxDismissed(msg InboxDismissedMsg) {
	if msg.Err == nil || msg.Silent || inboxDismissGone(msg.Err) {
		return
	}
	m.ShowNotification("The dismiss did not go through: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
}

// inboxNeedsYou reports whether an item is something a person has to act on,
// which is what the next-attention key visits. A finished turn is news, not a
// request, so it is left to the Inbox.
func inboxNeedsYou(it session.AttentionItem) bool {
	return it.Kind != session.AttentionFinished && it.Host == ""
}

// JumpToNextAttention goes to the oldest item that needs the person, in Inbox
// order. Pressed again soon after, it goes to the next one, and past the last
// it starts over, so a run of presses walks everything waiting. It reports
// whether there was anywhere to go.
func (m *OS) JumpToNextAttention() tea.Cmd {
	st := &m.Inbox
	var todo []session.AttentionItem
	for _, it := range st.Items {
		if inboxNeedsYou(it) {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		msg := "Nothing is waiting for you"
		if !st.Live {
			msg = "The Inbox is not connected to the daemon"
		}
		m.ShowNotification(msg, "info", m.Settings.NotificationDuration)
		return nil
	}
	next := 0
	if st.lastJumpID != "" && time.Since(st.lastJumpAt) < inboxCycleWindow {
		for i, it := range todo {
			if it.ID == st.lastJumpID {
				next = (i + 1) % len(todo)
				break
			}
		}
	}
	it := todo[next]
	st.lastJumpID, st.lastJumpAt = it.ID, time.Now()
	if it.Kind == session.AttentionMail {
		return m.inboxOpenMail(it, false)
	}
	m.inboxJump(it)
	if len(todo) > 1 {
		m.ShowNotification(strconv.Itoa(next+1)+" of "+strconv.Itoa(len(todo))+" waiting: "+inboxWho(it)+" "+inboxKindWords(it), "info", m.Settings.NotificationDuration)
	}
	return nil
}

// inboxWait is how long an item has waited, in at most three cells.
func inboxWait(since int64, now time.Time) string {
	if since <= 0 {
		return "?"
	}
	d := max(now.Sub(time.Unix(0, since)), 0)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

// inboxKindGlyph is the mark an item row wears for its kind. The group
// heading already says the kind in words; the mark is for the eye.
func inboxKindGlyph(kind string) string {
	if overlay.UseASCII() {
		switch kind {
		case session.AttentionApproval:
			return "!"
		case session.AttentionQuestion:
			return "?"
		case session.AttentionMail:
			return "@"
		case session.AttentionErrored:
			return "x"
		default:
			return "*"
		}
	}
	switch kind {
	case session.AttentionApproval, session.AttentionQuestion:
		return agentStateIndicator("needs_input")
	case session.AttentionMail:
		return sidebarMailGlyph()
	case session.AttentionErrored:
		return agentStateIndicator("errored")
	default:
		return agentStateIndicator("done")
	}
}
