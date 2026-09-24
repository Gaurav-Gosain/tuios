package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
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
	// Select narrows the overlay to the items a selector matches, empty for
	// every item. See inbox_select.go.
	Select   string
	selector *session.Selector
	// selectEditing, selectDraft and selectErr are the selector line: open,
	// what is typed on it, and why it did not parse.
	selectEditing bool
	selectDraft   string
	selectErr     string
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
	// poppedAt is when a question opened the Inbox by itself, zero when the
	// person opened it. No key acts for inboxAnswerSettle after a pop.
	poppedAt time.Time
	// paneKeyAt is when the person last typed into a pane. A question does
	// not pop within inboxPopQuiet of it.
	paneKeyAt time.Time

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
	// announcedResume holds the resume items this client has already told
	// its user about, so a restore is announced once and not on every
	// fresh listing.
	announcedResume map[string]bool
	// life is the snoozed items, the snooze picker, undo and the walk of
	// finished turns. See inbox_lifecycle.go.
	life inboxLifecycle
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
	// ID, Kind and Who name the item, so a dismiss that went through can be
	// offered for undo.
	ID   string
	Kind string
	Who  string
	Err  error
	// Silent marks a dismiss the client sent on its own, such as a finished
	// turn seen under the person's eyes. Its failure is never shown: the
	// person did not ask for it, and the next event or listing corrects the
	// mirror either way.
	Silent bool
}

// InboxResumedMsg is the answer to a resume: the command typed, or why not.
type InboxResumedMsg struct {
	Command string
	Err     error
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

	// The snoozed items are listed after the open ones, for the Inbox's
	// Snoozed group. A daemon from before snoozing refuses the parameter,
	// and has nothing snoozed, so it is asked without it.
	raw, err := client.CallWithTimeout("list-attention", map[string]any{"include_snoozed": true}, 5*time.Second)
	var callErr *session.VerbCallError
	if errors.As(err, &callErr) && callErr.Code == session.ErrVerbInvalidParams {
		raw, err = client.CallWithTimeout("list-attention", map[string]any{}, 5*time.Second)
	}
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
		m.noteAgentsSeen()
	case InboxEventsMsg:
		cmd = m.applyInboxEvents(inner)
		m.noteAgentsSeen()
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
	open, snoozed := splitSnoozed(msg.Items)
	st.Items = append(st.Items[:0], open...)
	st.life.Snoozed = append(st.life.Snoozed[:0], snoozed...)
	session.SortAttention(st.Items)
	st.Live = true
	st.Unsupported = false
	m.inboxChanged()
	m.announceResumes(st.Items)
}

// announceResumes says once, in the dock, that a restart left conversations
// to resume, and how to answer. A restore happens with nobody attached, so the
// items are usually already in the first listing a client gets, where no event
// would announce them. The alert policy does not govern it: it is not an agent
// changing state but the one time the person can bring the agents back.
func (m *OS) announceResumes(items []session.AttentionItem) {
	st := &m.Inbox
	var fresh []session.AttentionItem
	for _, it := range items {
		if it.Kind != session.AttentionResume || it.Host != "" || st.announcedResume[it.ID] {
			continue
		}
		if st.announcedResume == nil {
			st.announcedResume = make(map[string]bool)
		}
		st.announcedResume[it.ID] = true
		fresh = append(fresh, it)
	}
	if len(fresh) == 0 {
		return
	}
	text := m.inboxWhere(fresh[0]) + ": " + inboxWho(fresh[0]) + " " + inboxKindWords(fresh[0])
	if len(fresh) > 1 {
		text = strconv.Itoa(len(fresh)) + " agent conversations can be resumed"
	}
	text += agentAlertSep() + "open the Inbox with " + m.pressFor("prefix_inbox", "the prefix key then i") +
		", and press " + m.inboxKeyOr(config.ActionInboxResume, "y")
	m.ShowNotificationFrom(text, "info", m.Settings.NotificationDuration,
		NotifTarget{SessionID: fresh[0].Session, WindowID: fresh[0].Window})
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
		if m.applySnoozedEvent(ev.Action, *it) {
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
			// A question about the pane in front of the person opens the
			// Inbox on it, which is the popup: it needs no alert.
			if ev.Action == session.AttentionOpened && m.inboxPopAsk(*it) {
				continue
			}
			// An item the person just woke, restored or marked unread here
			// opens because they asked: it is not news to them.
			if (ev.Action == session.AttentionOpened || more) && !m.inboxAskedFor(*it) {
				alert = append(alert, it.ID)
			}
			cmds = append(cmds, m.inboxSeenUnderEyes(*it))
		}
	}
	session.SortAttention(st.Items)
	m.inboxChanged()
	m.announceResumes(st.Items)
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

// inboxPopQuiet is how long the person must not have typed into a pane before
// a question may open the Inbox by itself. Someone typing to the agent that
// just asked would otherwise have the rest of their keys read as Inbox keys.
const inboxPopQuiet = 600 * time.Millisecond

// inboxPopAsk opens the Inbox on a question ask-human put to the person, when
// it is about the pane this client shows. That is the popup: the person is
// looking at the agent that asked, so the question comes to them. It returns
// false, and the question alerts and waits in the Inbox instead, whenever the
// popup could take keys meant for something else:
//
//   - the question is about any other pane;
//   - an overlay is open, the Inbox included: its cursor, a peek or a text
//     line the person is typing in stays where it is;
//   - the person typed into a pane within inboxPopQuiet.
//
// After a pop no key acts for inboxAnswerSettle (InboxPopSettling), so a key
// already on its way to the pane does not dismiss, answer or close the
// question.
func (m *OS) inboxPopAsk(it session.AttentionItem) bool {
	if it.Kind != session.AttentionAsk || it.Window == "" || !m.inboxAttached(it) {
		return false
	}
	w := m.GetFocusedWindow()
	if w == nil || w.ID != it.Window || m.AnyOverlayOpen() {
		return false
	}
	now := time.Now()
	if !m.Inbox.paneKeyAt.IsZero() && now.Sub(m.Inbox.paneKeyAt) < inboxPopQuiet {
		return false
	}
	m.OpenInbox(session.AttentionAsk)
	m.Inbox.SelectedID = it.ID
	m.Inbox.poppedAt = now
	return true
}

// NotePaneKey records that the person typed into a pane just now, so a
// question does not pop under their hands.
func (m *OS) NotePaneKey() {
	m.Inbox.paneKeyAt = time.Now()
}

// InboxPopSettling reports whether the Inbox opened by itself on a question
// less than inboxAnswerSettle ago. Every key is then dropped with a word,
// because it was most likely typed for the pane before the question showed.
func (m *OS) InboxPopSettling() bool {
	st := &m.Inbox
	if !m.ShowInbox || st.poppedAt.IsZero() || time.Since(st.poppedAt) >= inboxAnswerSettle {
		return false
	}
	m.ShowNotification("A question just appeared. Read it, then answer", "info", m.Settings.NotificationDuration)
	return true
}

// inboxAlertState is the agent state whose alert policy governs a kind.
func inboxAlertState(kind string) string {
	switch kind {
	case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionAsk:
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
	if it.Stale {
		return false
	}
	// No agent state moves when a question is asked, so the state sync says
	// nothing about it even in the attached session: the Inbox does.
	if it.Kind == session.AttentionAsk {
		return policy.Alerts(inboxAlertState(it.Kind))
	}
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
		if it, ok := m.inboxItem(id); ok && (it.Kind == session.AttentionAsk || !m.inboxAttached(it)) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return
	}
	session.SortAttention(items)
	first := items[0]
	text := m.inboxAlertText(items)
	sev := "info"
	cue, cueState := sound.CueDone, "done"
	for _, it := range items {
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionMail, session.AttentionAsk:
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
		m.showAgentNotification(text, sev, inboxAlertMarkState(items, sev), m.Settings.NotificationDuration,
			NotifTarget{Host: inboxItemMachine(first), SessionID: first.Session, WindowID: first.Window})
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

// inboxAlertMarkState is the agent state whose mark the dock draws for a
// burst of Inbox alerts at severity sev: the state that severity stands for.
// Mail is not an agent state, so a burst of mail alone keeps the severity
// mark and reports no state.
func inboxAlertMarkState(items []session.AttentionItem, sev string) string {
	switch sev {
	case "error":
		return "errored"
	case "success":
		return "done"
	case "warning":
		for _, it := range items {
			if it.Kind != session.AttentionMail && inboxAlertState(it.Kind) == "needs_input" {
				return "needs_input"
			}
		}
	}
	return ""
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
	case session.AttentionPlan:
		return "has a plan to approve"
	case session.AttentionQuestion, session.AttentionAsk:
		return "has a question"
	case session.AttentionErrored:
		return "errored"
	case session.AttentionFinished:
		return sidebarStateWords("done")
	case session.AttentionMail:
		return "wrote to you"
	case session.AttentionResume:
		return "can resume its conversation"
	case session.AttentionOutbox:
		return "mail waits to be sent"
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
// this one. A session of this machine is named as the rail names it
// (sessionTitle); another machine's session keeps the name that machine sent.
func (m *OS) inboxWhere(it session.AttentionItem) string {
	if it.Kind == session.AttentionOutbox {
		return "for " + printableTitle(it.ForHost)
	}
	if it.Host != "" {
		return printableTitle(it.Host) + ":" + printableTitle(it.Session)
	}
	return printableTitle(m.sessionTitle(it.Session))
}

// inboxAlertText is the dock line for a burst: one item in full, naming its
// session, or a count and the sessions for several.
func (m *OS) inboxAlertText(items []session.AttentionItem) string {
	if len(items) == 1 {
		it := items[0]
		text := m.inboxWhere(it) + ": " + inboxWho(it) + " " + inboxKindWords(it)
		if note := printableTitle(it.Summary); note != "" {
			text += agentAlertSep() + note
		}
		return text
	}
	var sessions []string
	seen := map[string]bool{}
	for _, it := range items {
		where := m.inboxWhere(it)
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
// session on this machine, empty for every session on every machine. An item
// of a machine that cannot be reached is not counted: it is what that machine
// said last, and a figure drawn from it would report a moment that has passed.
func (m *OS) inboxCounts(sessionName string) sidebarAgentCountInfo {
	var c sidebarAgentCountInfo
	rank := 0
	for _, it := range m.Inbox.Items {
		if it.Stale || (sessionName != "" && (it.Host != "" || it.Session != sessionName)) {
			continue
		}
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionErrored, session.AttentionAsk:
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
// rail lists it and every linked host the daemon streams, plus any agent rows
// of other machines the rail holds. Without one it counts the rows, as it
// always has.
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
	// note is a line of words under the list, such as how many items are
	// snoozed. It is never selected.
	note string
}

// inboxGroupTitle is the heading a kind's rows sit under. It is words, so the
// grouping reads without colour.
//
// A question an agent's prompt asks and one put with ask-human are the same
// thing to the person, a question to answer with a digit, so both sit under
// Questions. They were two groups, "Questions" and "Asked you". Finished
// turns sit under Done, the word every other surface uses for the state.
func inboxGroupTitle(kind string) string {
	switch inboxGroupKey(kind) {
	case session.AttentionApproval:
		return "Approvals"
	case session.AttentionPlan:
		return "Plans"
	case session.AttentionQuestion:
		return "Questions"
	case session.AttentionMail:
		return "Mail"
	case session.AttentionErrored:
		return "Errored"
	case session.AttentionResume:
		return "Resume"
	case session.AttentionFinished:
		return "Done"
	case session.AttentionOutbox:
		return "Waiting to send"
	}
	return kind
}

// inboxGroupKey is the group a kind's rows are listed under: its own, except
// that an ask-human question is a question.
func inboxGroupKey(kind string) string {
	if kind == session.AttentionAsk {
		return session.AttentionQuestion
	}
	return kind
}

// inboxFilterAdmits reports whether the Inbox's kind filter shows a kind. The
// Questions filter shows both kinds of question; a filter on ask alone, which
// is what a question popping the Inbox opens with, shows only those.
func inboxFilterAdmits(filter, kind string) bool {
	return filter == "" || filter == kind || filter == inboxGroupKey(kind)
}

// inboxRows is the overlay's lines: each kind's heading and its items, oldest
// first, narrowed to the filter.
func (m *OS) inboxRows() []inboxRow {
	st := &m.Inbox
	var rows []inboxRow
	for i := 0; i < len(st.Items); {
		group := inboxGroupKey(st.Items[i].Kind)
		j := i
		for j < len(st.Items) && inboxGroupKey(st.Items[j].Kind) == group {
			j++
		}
		var items []inboxRow
		for k := i; k < j; k++ {
			if inboxFilterAdmits(st.Filter, st.Items[k].Kind) && m.inboxItemSelected(st.Items[k]) {
				items = append(items, inboxRow{item: &st.Items[k]})
			}
		}
		if len(items) > 0 {
			// Two kinds can share a group, each sorted oldest first by the
			// daemon; the group as a whole is kept oldest first too.
			slices.SortStableFunc(items, func(a, b inboxRow) int { return cmp.Compare(a.item.Since, b.item.Since) })
			rows = append(rows, inboxRow{heading: inboxGroupTitle(group), count: len(items)})
			rows = append(rows, items...)
		}
		i = j
	}
	return m.inboxSnoozedRows(rows)
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
	st.poppedAt = time.Time{}
	m.clampInboxSelection()
}

// CloseInbox hides the Inbox, and the peek with it.
func (m *OS) CloseInbox() {
	m.ShowInbox = false
	m.Inbox.Peek = nil
	m.Inbox.life.pickFor, m.Inbox.life.closeAfterPick = "", false
}

// InboxMove moves the cursor by delta items, stepping over group headings.
func (m *OS) InboxMove(delta int) {
	rows := m.inboxRows()
	st := &m.Inbox
	if len(rows) == 0 || delta == 0 {
		return
	}
	st.Selected = m.listStepSkip(st.Selected, delta, len(rows),
		func(i int) bool { return rows[i].item != nil })
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
	// A question put with ask-human is shown under Questions, so the filter
	// steps over it and Questions shows both.
	if next == session.AttentionAsk {
		if i := session.AttentionKindRank(next); i+1 < len(session.AttentionKindNames) {
			next = session.AttentionKindNames[i+1]
		}
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
	if it.Kind == session.AttentionOutbox {
		// Mail waiting for another machine has no pane to go to. It goes on
		// its own when the link is back.
		m.ShowNotification("This mail goes to "+printableTitle(it.ForHost)+" when its link is back. d discards it.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.CloseInbox()
	if it.Kind == session.AttentionMail {
		return m.inboxOpenMail(it, false)
	}
	if it.Kind == session.AttentionAsk {
		// A question has no prompt in its pane to answer: going there leaves
		// it waiting here. A question from outside every pane has no pane.
		if it.Window != "" {
			m.inboxJump(it)
		}
		return nil
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

// InboxNumber is a digit key in the Inbox list. On a question ask-human put,
// it picks that answer. On a held approval 1, 2 and 3 are allow once, always
// allow and deny, the order of the harness's own menu.
func (m *OS) InboxNumber(n int) tea.Cmd {
	if it, ok := m.inboxSelected(); ok && it.Kind == session.AttentionAsk {
		return m.InboxAnswerAsk(n)
	}
	if n >= 1 && n <= len(inboxAnswerOrder) {
		return m.InboxReplyApproval(inboxAnswerOrder[n-1].decision)
	}
	return nil
}

// InboxAnswerAsk answers the selected question with its nth option, counting
// from 1. Like an approval, the question must have been on screen, as it is,
// for a moment before a key answers it.
func (m *OS) InboxAnswerAsk(n int) tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok || it.Kind != session.AttentionAsk || it.RequestID == "" {
		return nil
	}
	if n < 1 || n > len(it.Options) {
		m.ShowNotification("This question takes 1 to "+strconv.Itoa(len(it.Options)), "info", m.Settings.NotificationDuration)
		return nil
	}
	if !inboxShowsWhole(it) {
		m.ShowNotification("This question has characters this terminal cannot show, so it is not answered here", "info", m.Settings.NotificationDuration)
		return nil
	}
	if !m.inboxAnswerSettled(it, time.Now()) {
		m.ShowNotification("This question just appeared. Read it, then answer", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.inboxAnswerAskCmd(it, it.Options[n-1])
}

// InboxAskAnsweredMsg is the answer to an answer-ask.
type InboxAskAnsweredMsg struct {
	Name   string
	Answer string
	// Standing is the answer that stands, an earlier one's when Applied is
	// false.
	Standing string
	Applied  bool
	Reason   string
	Err      error
}

// inboxAnswerAskCmd is the answer-ask call, with this client's attach nonce,
// which is what lets the daemon take the answer as the person's.
func (m *OS) inboxAnswerAskCmd(it session.AttentionItem, answer string) tea.Cmd {
	if m.DaemonClient == nil || m.AttachedHost != "" || it.Host != "" {
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
			return InboxAskAnsweredMsg{Name: name, Answer: answer, Err: err}
		}
		defer func() { _ = client.Close() }()
		raw, err := client.CallWithTimeout("answer-ask", map[string]any{
			"request_id":  it.RequestID,
			"answer":      answer,
			"human_nonce": nonce,
			// The question the person read. The daemon answers nothing when
			// it is not the one asked.
			"question": it.Summary,
		}, 5*time.Second)
		if err != nil {
			return InboxAskAnsweredMsg{Name: name, Answer: answer, Err: err}
		}
		var res struct {
			Answer  string `json:"answer"`
			Applied bool   `json:"applied"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return InboxAskAnsweredMsg{Name: name, Answer: answer, Err: err}
		}
		return InboxAskAnsweredMsg{Name: name, Answer: answer, Standing: res.Answer, Applied: res.Applied, Reason: res.Reason}
	}
}

// applyInboxAskAnswered says what became of an answer.
func (m *OS) applyInboxAskAnswered(msg InboxAskAnsweredMsg) {
	switch {
	case msg.Err != nil:
		m.ShowNotification("The answer did not go through: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
	case !msg.Applied && msg.Reason == inboxReplyChanged:
		m.ShowNotification(msg.Name+" is asking something else now, so nothing was answered. Read it again", "info", m.Settings.NotificationDuration*2)
	case !msg.Applied && msg.Standing != "":
		m.ShowNotification(msg.Name+" was already answered: "+printableTitle(msg.Standing), "info", m.Settings.NotificationDuration)
	case !msg.Applied:
		m.ShowNotification(msg.Name+"'s question ended before the answer: "+strings.ReplaceAll(msg.Reason, "_", " "), "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification(msg.Name+": "+printableTitle(msg.Answer), "success", m.Settings.NotificationDuration)
	}
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

// inboxItemMachine is the machine an item is on, as attachedMachine names it.
func inboxItemMachine(it session.AttentionItem) string {
	if it.Host == "" {
		return federation.LocalHostName
	}
	return it.Host
}

// inboxReach attaches the machine and the session an item is on, when the
// client is on another machine, and reports whether the client is now on the
// item's machine. A machine that cannot be reached says so, with when it was
// last heard from, and nothing is given up.
func (m *OS) inboxReach(it session.AttentionItem) bool {
	target := inboxItemMachine(it)
	if target == m.attachedMachine() {
		return true
	}
	if it.Stale || !m.hostIsUp(target) {
		m.ShowNotification(printableTitle(target)+" cannot be reached ("+inboxSeen(it.SeenAt, time.Now())+")", "warning", m.Settings.NotificationWarningDuration)
		return false
	}
	if err := m.SwitchToHostSession(target, it.Session, false); err != nil {
		m.ShowNotification(hostAttachRefusal(target, err), "error", m.Settings.NotificationDuration*3)
		return false
	}
	return true
}

// inboxJump lands on an item's pane, attaching its machine first when it is on
// another one.
func (m *OS) inboxJump(it session.AttentionItem) {
	if !m.inboxReach(it) {
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
		if cmd, ok := m.inboxReplyAgent(it); ok {
			return cmd
		}
		m.ShowNotification(m.inboxKeyOr(config.ActionInboxReply, "r")+" replies to mail. Enter goes to the pane.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.CloseInbox()
	return m.inboxOpenMail(it, true)
}

// inboxOpenMail opens a mail item's thread, in its session, with the reply
// line when reply is set. A thread in another session waits for that
// session's mail to load.
func (m *OS) inboxOpenMail(it session.AttentionItem, reply bool) tea.Cmd {
	if inboxItemMachine(it) != m.attachedMachine() {
		// Attaching the machine lands on the session and loads its mail, so
		// the thread opens once that has arrived.
		if !m.inboxReach(it) {
			return nil
		}
		m.Inbox.pendingThread, m.Inbox.pendingReply = it.Thread, reply
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
	cmd := m.inboxDismissCmd(it.ID, false)
	if cmd == nil {
		return nil
	}
	id, kind, who := it.ID, it.Kind, inboxWho(it)
	return func() tea.Msg {
		msg, _ := cmd().(InboxDismissedMsg)
		msg.ID, msg.Kind, msg.Who = id, kind, who
		return msg
	}
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

// InboxRelease passes the selected held mail on to the agent it was for:
// mail from another machine whose link policy holds it waits in the Inbox
// until the person reads it and decides. It sends release-agent-message with
// this client's attach nonce, which is what lets the daemon tell the person
// from an agent.
func (m *OS) InboxRelease() tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	if it.HeldID == 0 {
		m.ShowNotification("p passes on mail another machine sent an agent here, held for you. This item holds none.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if it.Host != "" || m.AttachedHost != "" || m.DaemonClient == nil {
		m.ShowNotification("That mail is held on another machine; attach there to pass it on", "info", m.Settings.NotificationDuration)
		return nil
	}
	nonce := m.DaemonClient.HumanNonce()
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	build := m.DaemonClient.ClientVersion()
	sessionName, id, heldFor := it.Session, it.HeldID, it.HeldFor
	return func() tea.Msg {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return InboxReleasedMsg{Err: err}
		}
		defer func() { _ = client.Close() }()
		_, err = client.CallWithTimeout("release-agent-message", map[string]any{"session": sessionName, "id": id, "human_nonce": nonce}, 5*time.Second)
		return InboxReleasedMsg{To: heldFor, Err: err}
	}
}

// InboxReleasedMsg is the answer to a release.
type InboxReleasedMsg struct {
	To  string
	Err error
}

// applyInboxReleased says where the mail went, or why it did not.
func (m *OS) applyInboxReleased(msg InboxReleasedMsg) {
	if msg.Err != nil {
		m.ShowNotification("The mail was not passed on: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	m.ShowNotification("Passed on to "+printableTitle(msg.To), "success", m.Settings.NotificationDuration)
}

// InboxResume answers the selected resume item: it goes to the pane and has
// the daemon type the conversation's resume command there, so the person
// watches the agent come back. The item closes when the daemon says the
// command was typed.
func (m *OS) InboxResume() tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	if it.Kind != session.AttentionResume {
		m.ShowNotification("y resumes a conversation a restart left. Enter goes to the pane.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if it.Host != "" || m.AttachedHost != "" || m.DaemonClient == nil {
		m.ShowNotification("That pane is on another machine; attach there to resume it", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.CloseInbox()
	m.inboxJump(it)
	build := m.DaemonClient.ClientVersion()
	sessionName, window := it.Session, it.Window
	return func() tea.Msg {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return InboxResumedMsg{Err: err}
		}
		defer func() { _ = client.Close() }()
		raw, err := client.CallWithTimeout("resume-agent", map[string]any{"session": sessionName, "window": window}, 5*time.Second)
		if err != nil {
			return InboxResumedMsg{Err: err}
		}
		var res struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(raw, &res)
		return InboxResumedMsg{Command: res.Command}
	}
}

// applyInboxResumed says what was typed, or why nothing was.
func (m *OS) applyInboxResumed(msg InboxResumedMsg) {
	if msg.Err != nil {
		m.ShowNotification("The resume did not go through: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	m.ShowNotification("Resumed: "+printableTitle(msg.Command), "success", m.Settings.NotificationDuration)
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
	if msg.Err == nil && !msg.Silent && msg.ID != "" {
		m.noteInboxDismissed(msg.ID, msg.Kind, msg.Who)
		return
	}
	if msg.Err == nil || msg.Silent || inboxDismissGone(msg.Err) {
		return
	}
	m.ShowNotification("The dismiss did not go through: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
}

// inboxNeedsYou reports whether an item is something a person has to act on,
// which is what the next-attention key visits. A finished turn is news, not a
// request, so it is left to the Inbox.
func inboxNeedsYou(it session.AttentionItem) bool {
	return it.Kind != session.AttentionFinished && it.Kind != session.AttentionOutbox && !it.Stale
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
	if it.Kind == session.AttentionAsk {
		// A question is answered in the Inbox, not in its pane.
		m.OpenInbox(session.AttentionAsk)
		m.Inbox.SelectedID = it.ID
		m.clampInboxSelection()
		return nil
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

// inboxSeen is when a machine that cannot be reached was last heard from, as
// the Inbox and the rail say it: "seen 3m ago", or "offline" when it never was.
func inboxSeen(seenAt int64, now time.Time) string {
	if seenAt <= 0 {
		return "offline"
	}
	return "seen " + inboxWait(seenAt, now) + " ago"
}

// inboxKindGlyph is the mark an item row wears for its kind. The group
// heading already says the kind in words; the mark is for the eye.
//
// A kind that stands for an agent state wears that state's mark, in both
// glyph modes, so an approval here is the same mark as the pane's row on the
// rail. The ASCII forms of the other kinds are chosen not to collide with any
// state's: finished used to be "*", which is working's.
func inboxKindGlyph(kind string) string {
	switch kind {
	case session.AttentionOutbox:
		if overlay.UseASCII() {
			return "^"
		}
		return "↑"
	case session.AttentionMail:
		return sidebarMailGlyph()
	case session.AttentionResume:
		if overlay.UseASCII() {
			return ">"
		}
		return "↻"
	}
	return agentStateIndicator(inboxAlertState(kind))
}
