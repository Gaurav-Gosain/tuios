package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The copies between machines on the client: a mirror of the daemon's
// transfers (session/transfer.go), the dock item and the rail mark drawn from
// it, the Copies panel that reads it, and the notices a copy raises when it
// ends.
//
// Rule one: nothing is drawn while no copy exists. Every element appears with
// the first copy and goes away transferLinger after the last one ends. A
// failed copy keeps the dock item until the person has seen it in the panel.
//
// The mirror is fed by one watcher goroutine per client, as the Inbox's is.
// It subscribes to the transfer events first, then lists the copies on a
// second connection, so a change between the two is not lost. An idle client
// costs one blocked read: no timer runs while no copy moves, and the one timer
// there is, the linger after the last copy ends, fires once.

// transferQueue bounds the channel from the watcher to Update.
const transferQueue = 16

// transferBatch is how long the watcher gathers events after the first one
// of a burst. Progress comes at most four times a second per copy, so a
// handful of copies is one render per batch.
const transferBatch = 100 * time.Millisecond

// transferLinger is how long the dock item and the rail mark stay after the
// last copy ends.
const transferLinger = 10 * time.Second

// transferRetryMax caps the wait between attempts to reach the daemon.
const transferRetryMax = 30 * time.Second

// transferCallTimeout bounds one transfer verb call.
const transferCallTimeout = 15 * time.Second

// transfersState is the mirror, the Copies panel and its dialogs.
type transfersState struct {
	// rows is every copy the daemon lists, newest first.
	rows []session.TransferRow
	// live is true while the watcher holds a current listing.
	live bool
	// unsupported is true when the daemon has no transfers.
	unsupported bool
	// seen holds the failed copies the person has seen in the panel. A
	// failed copy keeps the dock item until it is here.
	seen map[string]bool
	// lastEnd is when the last copy ended, for the linger.
	lastEnd time.Time
	// lingerGen numbers the linger timers, so only the newest one acts.
	lingerGen uint64

	// events carries what the watcher reads, and stop ends it.
	events chan tea.Msg
	stop   func()

	// open, selected, scroll and selectedID are the Copies panel. The
	// cursor follows selectedID: the list is sorted again on every event.
	open       bool
	selected   int
	scroll     int
	selectedID string

	// dialog is the open question: cancel, conflict, send or drop.
	dialog *copyDialog

	// drops are dropped files whose copies have not ended yet. Their far
	// paths are pasted when every copy is checked.
	drops []*pendingDrop

	// call replaces the daemon in tests.
	call inboxVerbCall
}

// transferEvent is one transfer or transfer-progress event.
type transferEvent struct {
	Type   string
	Action string
	Row    session.TransferRow
}

// transfersSnapshotMsg is a fresh listing, with the events that came between
// the subscribe and the listing.
type transfersSnapshotMsg struct {
	Rows  []session.TransferRow
	Early []transferEvent
}

// transfersEventsMsg is a batch of events from the watcher.
type transfersEventsMsg struct {
	Events []transferEvent
}

// transfersDownMsg says the watcher lost the daemon. Unsupported says the
// daemon answered and has no transfers, and the watcher stopped for good.
type transfersDownMsg struct {
	Err         error
	Unsupported bool
}

// transfersWatchMsg wraps what the watcher delivers, so Update can re-arm the
// listener for exactly these messages.
type transfersWatchMsg struct {
	msg tea.Msg
}

// transfersLingerMsg is the linger after the last copy ending running out.
type transfersLingerMsg struct {
	gen uint64
}

// transferCallMsg is the answer to a pause, resume, cancel, answer or start.
type transferCallMsg struct {
	Verb string
	Name string
	Row  *session.TransferRow
	Err  error
}

// transfersClearedMsg is the answer to a clear.
type transfersClearedMsg struct {
	IDs []string
	Err error
}

// errTransferGap ends one watch so the next one starts from a fresh listing.
var errTransferGap = errors.New("the transfer stream has a gap")

// transferEnded reports whether a copy is over: done, failed or cancelled.
func transferEnded(state string) bool {
	return state == "done" || state == "failed" || state == "cancelled"
}

// startTransfersWatch starts the watcher and returns the command that waits
// for its first delivery. The copies are this machine's daemon's, whichever
// session the client is attached to.
func (m *OS) startTransfersWatch() tea.Cmd {
	if !m.watchesInbox() || m.transfers.events != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, transferQueue)
	m.transfers.events, m.transfers.stop = ch, cancel
	build := m.DaemonClient.ClientVersion()
	go runTransfersWatch(ctx, func() (*session.VerbClient, error) { return session.DialVerbClientAs(build) }, ch)
	return listenForTransfers(ch)
}

// endTransfersWatch stops the watcher.
func (m *OS) endTransfersWatch() {
	if m.transfers.stop != nil {
		m.transfers.stop()
		m.transfers.stop = nil
	}
}

// listenForTransfers waits for one delivery from the watcher.
func listenForTransfers(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return transfersWatchMsg{msg: msg}
	}
}

// runTransfersWatch keeps a watch running until ctx ends, starting over after
// every failure with a growing wait.
func runTransfersWatch(ctx context.Context, dial inboxDial, out chan<- tea.Msg) {
	defer close(out)
	wait := time.Second
	for ctx.Err() == nil {
		listed, err := transfersWatchOnce(ctx, dial, out)
		if ctx.Err() != nil {
			return
		}
		var callErr *session.VerbCallError
		if errors.As(err, &callErr) && (callErr.Code == session.ErrVerbUnknownVerb || callErr.Code == session.ErrVerbInvalidParams) {
			inboxSend(ctx, out, transfersDownMsg{Err: err, Unsupported: true})
			return
		}
		inboxSend(ctx, out, transfersDownMsg{Err: err})
		if listed {
			wait = time.Second
		}
		if errors.Is(err, errTransferGap) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, transferRetryMax)
	}
}

// transfersWatchOnce subscribes, lists, and delivers events until the
// connection fails or the stream has a gap. It reports whether the listing
// was delivered.
func transfersWatchOnce(ctx context.Context, dial inboxDial, out chan<- tea.Msg) (bool, error) {
	client, err := dial()
	if err != nil {
		return false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	defer func() { _ = client.Close() }()

	// Subscribed first, so a copy that changes while the list is read is
	// still heard about.
	if _, err := client.CallWithTimeout("subscribe", map[string]any{
		"types": []string{session.EventTransfer, session.EventTransferProgress},
	}, 5*time.Second); err != nil {
		return false, err
	}

	type read struct {
		ev  transferEvent
		gap bool
		err error
	}
	lines := make(chan read, 64)
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
				Type     string               `json:"type"`
				Action   string               `json:"action"`
				Transfer *session.TransferRow `json:"transfer"`
			}
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			switch {
			case ev.Type == session.EventGap:
				put(read{gap: true})
				return
			case (ev.Type == session.EventTransfer || ev.Type == session.EventTransferProgress) && ev.Transfer != nil:
				if !put(read{ev: transferEvent{Type: ev.Type, Action: ev.Action, Row: *ev.Transfer}}) {
					return
				}
			}
		}
	}()

	lister, err := dial()
	if err != nil {
		return false, err
	}
	raw, err := lister.CallWithTimeout("transfer-list", map[string]any{}, 10*time.Second)
	_ = lister.Close()
	if err != nil {
		return false, err
	}
	var listing struct {
		Transfers []session.TransferRow `json:"transfers"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return false, err
	}
	// What came while the list was read.
	var early []transferEvent
drain:
	for {
		select {
		case r, ok := <-lines:
			switch {
			case !ok:
				return false, errors.New("the transfer stream ended")
			case r.err != nil:
				return false, r.err
			case r.gap:
				return false, errTransferGap
			}
			early = append(early, r.ev)
		default:
			break drain
		}
	}
	if !inboxSend(ctx, out, transfersSnapshotMsg{Rows: listing.Transfers, Early: early}) {
		return true, ctx.Err()
	}

	for {
		first, ok := <-lines
		if !ok {
			return true, errors.New("the transfer stream ended")
		}
		if first.err != nil {
			return true, first.err
		}
		if first.gap {
			return true, errTransferGap
		}
		batch := []transferEvent{first.ev}
		timer := time.NewTimer(transferBatch)
		var end error
	gather:
		for {
			select {
			case r, ok := <-lines:
				switch {
				case !ok:
					end = errors.New("the transfer stream ended")
					break gather
				case r.err != nil:
					end = r.err
					break gather
				case r.gap:
					end = errTransferGap
					break gather
				}
				batch = append(batch, r.ev)
			case <-timer.C:
				break gather
			}
		}
		timer.Stop()
		if !inboxSend(ctx, out, transfersEventsMsg{Events: batch}) {
			return true, ctx.Err()
		}
		if end != nil {
			return true, end
		}
	}
}

// handleTransfersWatch applies one delivery and re-arms the listener.
func (m *OS) handleTransfersWatch(msg transfersWatchMsg) tea.Cmd {
	var cmd tea.Cmd
	switch inner := msg.msg.(type) {
	case transfersSnapshotMsg:
		m.applyTransfersSnapshot(inner)
	case transfersEventsMsg:
		cmd = m.applyTransferEvents(inner.Events)
	case transfersDownMsg:
		m.transfers.live = false
		m.transfers.unsupported = inner.Unsupported
		if inner.Unsupported {
			return nil
		}
	case nil:
		return nil
	}
	return tea.Batch(cmd, listenForTransfers(m.transfers.events))
}

// newerTransferRow picks the later of two rows of one copy, for an event that
// came while the listing was read and may be older or newer than it. A copy
// that ended outranks one that has not, and more bytes outrank fewer.
func newerTransferRow(a, b session.TransferRow) session.TransferRow {
	switch ae, be := transferEnded(a.State), transferEnded(b.State); {
	case ae && !be:
		return a
	case be && !ae:
		return b
	}
	if b.Done >= a.Done {
		return b
	}
	return a
}

// applyTransfersSnapshot replaces the mirror with a fresh listing.
func (m *OS) applyTransfersSnapshot(msg transfersSnapshotMsg) {
	st := &m.transfers
	byID := make(map[string]session.TransferRow, len(msg.Rows))
	for _, r := range msg.Rows {
		byID[r.ID] = r
	}
	for _, ev := range msg.Early {
		if transferRemovedEvent(ev) {
			delete(byID, ev.Row.ID)
			continue
		}
		if r, ok := byID[ev.Row.ID]; ok {
			byID[ev.Row.ID] = newerTransferRow(r, ev.Row)
		} else if ev.Type == session.EventTransfer {
			byID[ev.Row.ID] = ev.Row
		}
	}
	st.rows = st.rows[:0]
	var lastEnd int64
	for _, r := range byID {
		st.rows = append(st.rows, r)
		if transferEnded(r.State) {
			lastEnd = max(lastEnd, r.Ended)
		}
	}
	sortTransferRows(st.rows)
	st.live = true
	st.unsupported = false
	if lastEnd > 0 {
		if at := time.UnixMilli(lastEnd); at.After(st.lastEnd) {
			st.lastEnd = at
		}
	}
	m.transfersChanged()
}

// sortTransferRows puts the newest copy first.
func sortTransferRows(rows []session.TransferRow) {
	slices.SortStableFunc(rows, func(a, b session.TransferRow) int {
		if c := cmp.Compare(b.Created, a.Created); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// transferRowIndex is the index of a copy in the mirror, or -1.
func (m *OS) transferRowIndex(id string) int {
	return slices.IndexFunc(m.transfers.rows, func(r session.TransferRow) bool { return r.ID == id })
}

// upsertTransferRow puts a row into the mirror and returns the row it
// replaced, if any.
func (m *OS) upsertTransferRow(row session.TransferRow) (prev session.TransferRow, had bool) {
	st := &m.transfers
	if i := m.transferRowIndex(row.ID); i >= 0 {
		prev, had = st.rows[i], true
		st.rows[i] = row
	} else {
		st.rows = append(st.rows, row)
	}
	sortTransferRows(st.rows)
	return prev, had
}

// applyTransferEvents folds a batch of events into the mirror. A copy that
// ends raises its notice, and the last one to end starts the linger.
func (m *OS) applyTransferEvents(events []transferEvent) tea.Cmd {
	var cmds []tea.Cmd
	for _, ev := range events {
		if transferRemovedEvent(ev) {
			m.dropTransferRow(ev.Row.ID)
			continue
		}
		cmds = append(cmds, m.applyTransferRow(ev.Row, ev.Type == session.EventTransferProgress))
	}
	m.transfersChanged()
	cmds = append(cmds, m.transfersLingerCmd())
	return tea.Batch(cmds...)
}

// transferRemovedEvent reports whether an event says a copy left the
// daemon's list: transfer-clear removed it, or it ended long enough ago.
func transferRemovedEvent(ev transferEvent) bool {
	return ev.Type == session.EventTransfer && ev.Action == "removed"
}

// dropTransferRow takes a copy out of the mirror. Nothing of it comes after:
// the daemon says a copy's end before its removal, and a progress event is
// never published after the end.
func (m *OS) dropTransferRow(id string) {
	m.transfers.rows = slices.DeleteFunc(m.transfers.rows, func(r session.TransferRow) bool { return r.ID == id })
}

// applyTransferRow folds one row into the mirror. progress says it came from
// a progress event, which never moves a copy that ended back: a progress
// event is not kept for replay, so one can arrive after the copy's end.
func (m *OS) applyTransferRow(row session.TransferRow, progress bool) tea.Cmd {
	if i := m.transferRowIndex(row.ID); i >= 0 && progress && transferEnded(m.transfers.rows[i].State) {
		return nil
	}
	prev, had := m.upsertTransferRow(row)
	ended := transferEnded(row.State) && (!had || !transferEnded(prev.State))
	if row.State == "failed" && m.transfers.open {
		m.markTransferSeen(row.ID)
	}
	if !ended {
		return nil
	}
	m.transfers.lastEnd = time.Now()
	if cmd, taken := m.dropCopyEnded(row); taken {
		return cmd
	}
	m.transferEndNotice(row)
	return nil
}

// transfersLingerCmd starts the linger once no copy is moving and one ended,
// so the dock item and the rail mark go away on their own. It is the only
// timer the copies use, and it fires once.
func (m *OS) transfersLingerCmd() tea.Cmd {
	st := &m.transfers
	if m.transfersActive() > 0 || st.lastEnd.IsZero() {
		return nil
	}
	left := transferLinger - time.Since(st.lastEnd)
	if left <= 0 {
		return nil
	}
	st.lingerGen++
	gen := st.lingerGen
	return tea.Tick(left+50*time.Millisecond, func(time.Time) tea.Msg { return transfersLingerMsg{gen: gen} })
}

// applyTransfersLinger redraws once the linger ran out.
func (m *OS) applyTransfersLinger(msg transfersLingerMsg) {
	if msg.gen != m.transfers.lingerGen {
		return
	}
	m.transfersChanged()
}

// transfersChanged records that the mirror moved, so the dock, the rail and
// the panel draw again.
func (m *OS) transfersChanged() {
	m.clampTransfersSelection()
	m.sidebarCache.invalidate()
	m.secondarySidebarCache.invalidate()
	m.cachedViewContent = ""
	m.renderSkipped = false
}

// transfersActive counts the copies that have not ended.
func (m *OS) transfersActive() int {
	n := 0
	for _, r := range m.transfers.rows {
		if !transferEnded(r.State) {
			n++
		}
	}
	return n
}

// transfersShown reports whether the dock item and the rail mark are drawn: a
// copy has not ended, a failed copy was not seen, or the last copy ended less
// than transferLinger ago.
func (m *OS) transfersShown() bool {
	st := &m.transfers
	for _, r := range st.rows {
		if !transferEnded(r.State) || r.State == "failed" && !st.seen[r.ID] {
			return true
		}
	}
	return !st.lastEnd.IsZero() && time.Since(st.lastEnd) < transferLinger
}

// markTransferSeen records that the person saw a failed copy in the panel.
func (m *OS) markTransferSeen(id string) {
	if m.transfers.seen == nil {
		m.transfers.seen = map[string]bool{}
	}
	m.transfers.seen[id] = true
}

// transferPlace names a machine of a copy for the person: the host, or "this
// machine".
func transferPlace(host string) string {
	if host == "" || host == federation.LocalHostName {
		return "this machine"
	}
	return printableTitle(host)
}

// transferEndNotice is the dock message for a copy that ended. A cancelled
// copy says nothing: the person cancelled it.
func (m *OS) transferEndNotice(row session.TransferRow) {
	d := m.Settings.NotificationDuration
	name := printableTitle(row.Name)
	switch row.State {
	case "done":
		verb := "Copied"
		if row.Move {
			verb = "Moved"
		}
		text := verb + " " + name + " to " + transferPlace(row.Dst.Host) + "."
		if row.Verified {
			text += " Checked."
		}
		if row.FailedFiles > 0 {
			text += " " + countOf(row.FailedFiles, "file") + " did not copy."
		}
		m.ShowNotification(text, "success", d)
	case "failed":
		text := transferFailedText(row)
		if how := m.transfersRetryHow(); how != "" {
			text += " " + how
		}
		m.ShowNotification(text, "error", d*2)
	}
}

// transferFailedText says why a copy stopped, in one or two sentences.
func transferFailedText(row session.TransferRow) string {
	text := "The copy of " + printableTitle(row.Name) + " to " + transferPlace(row.Dst.Host) + " stopped"
	if why := strings.TrimSpace(printableTitle(row.Error)); why != "" {
		text += ": " + strings.TrimSuffix(why, ".")
	}
	return text + "."
}

// transfersRetryHow says how to reach the retry, with the keys bound now:
// the chord that opens the panel, then the retry key. It is "" when nothing
// opens the panel by a key.
func (m *OS) transfersRetryHow() string {
	open := m.pressFor("prefix_transfers", "")
	if open == "" {
		return ""
	}
	if retry := m.transfersKey(config.ActionTransfersRetry); retry != "" {
		return "Press " + open + " then " + retry + " to try again."
	}
	return "Press " + open + " to see the copy."
}

// transferCaller is how the copies reach the daemon. Tests set call.
func (m *OS) transferCaller() inboxVerbCall {
	if m.transfers.call != nil {
		return m.transfers.call
	}
	return m.inboxCaller()
}

// transferCallCmd runs one transfer verb off the loop.
func (m *OS) transferCallCmd(verb, name string, params map[string]any) tea.Cmd {
	call := m.transferCaller()
	return func() tea.Msg {
		raw, err := call(verb, params, transferCallTimeout)
		msg := transferCallMsg{Verb: verb, Name: name, Err: err}
		if err == nil {
			var row session.TransferRow
			if json.Unmarshal(raw, &row) == nil && row.ID != "" {
				msg.Row = &row
			}
		}
		return msg
	}
}

// applyTransferCall takes the answer to a call. A row in it is folded in at
// once; its event comes too, and says the same.
func (m *OS) applyTransferCall(msg transferCallMsg) tea.Cmd {
	if msg.Err != nil {
		m.ShowNotification(transferCallFailed(msg), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	if msg.Row == nil {
		return nil
	}
	cmd := m.applyTransferRow(*msg.Row, false)
	m.transfersChanged()
	if msg.Verb == "transfer-start" {
		m.ShowNotification("The copy of "+printableTitle(msg.Row.Name)+" to "+transferPlace(msg.Row.Dst.Host)+" started.", "info", m.Settings.NotificationDuration)
	}
	return tea.Batch(cmd, m.transfersLingerCmd())
}

// transferCallFailed says what a call could not do.
func transferCallFailed(msg transferCallMsg) string {
	name := printableTitle(msg.Name)
	what := map[string]string{
		"transfer-pause":  "pause the copy of " + name,
		"transfer-resume": "go on with the copy of " + name,
		"transfer-cancel": "cancel the copy of " + name,
		"transfer-answer": "answer the copy of " + name,
		"transfer-start":  "start the copy of " + name,
		"transfer-clear":  "clear the copies",
		"file-mkdir":      "make the folder for " + name,
		"drop-files":      "send " + name,
	}[msg.Verb]
	if what == "" {
		what = "do that"
	}
	why := msg.Err.Error()
	var callErr *session.VerbCallError
	if errors.As(msg.Err, &callErr) && callErr.Message != "" {
		why = callErr.Message
	}
	return "tuios could not " + what + ": " + strings.TrimSuffix(printableTitle(why), ".") + "."
}

// clampTransfersSelection keeps the panel's cursor on the copy it was on.
func (m *OS) clampTransfersSelection() {
	st := &m.transfers
	if st.selectedID != "" {
		if i := m.transferRowIndex(st.selectedID); i >= 0 {
			st.selected = i
			return
		}
	}
	st.selected = clampInt(st.selected, 0, max(len(st.rows)-1, 0))
	if st.selected < len(st.rows) {
		st.selectedID = st.rows[st.selected].ID
	}
}

// selectedTransfer is the copy under the panel's cursor.
func (m *OS) selectedTransfer() (session.TransferRow, bool) {
	st := &m.transfers
	if st.selected < 0 || st.selected >= len(st.rows) {
		return session.TransferRow{}, false
	}
	return st.rows[st.selected], true
}

// TransfersOpen reports whether the Copies panel is up.
func (m *OS) TransfersOpen() bool { return m.transfers.open }

// TransfersOwnsKeys reports whether the Copies panel is up and nothing is
// over it. A panel opened on top of it, Settings by its prefix chord for one,
// is the one drawn in front, so it takes the keys: the Copies panel answering
// for it would close the wrong panel on esc.
//
// The stacking order is reconciled here because a panel opened by the key
// before this one has not been drawn yet. The overlays outside that order
// (the screenshot preview, the tape dialogs, the scrollback browser) only
// open over the panel, never under it, so any of them being up means the
// panel is behind.
func (m *OS) TransfersOwnsKeys() bool {
	if !m.transfers.open {
		return false
	}
	if m.ShotPreview.Open || m.ShowTapeManager || m.ShowTapeReview || m.ShowScrollbackBrowser {
		return false
	}
	m.reconcileOverlayZOrder()
	return m.topmostOverlayKind() == overlayKindTransfers
}

// OpenTransfers shows the Copies panel. The failed copies in it count as seen
// from now on, so the dock item they kept goes away with the linger.
func (m *OS) OpenTransfers() tea.Cmd {
	st := &m.transfers
	st.open = true
	st.selected, st.scroll, st.selectedID = 0, 0, ""
	if len(st.rows) > 0 {
		st.selectedID = st.rows[0].ID
	}
	seenAny := false
	for _, r := range st.rows {
		if r.State == "failed" && !st.seen[r.ID] {
			m.markTransferSeen(r.ID)
			seenAny = true
		}
	}
	if seenAny && st.lastEnd.IsZero() {
		st.lastEnd = time.Now()
	}
	m.transfersChanged()
	return m.transfersLingerCmd()
}

// CloseTransfers hides the Copies panel.
func (m *OS) CloseTransfers() {
	m.transfers.open = false
	m.transfersChanged()
}

// TransfersMove steps the panel's cursor.
func (m *OS) TransfersMove(delta int) {
	st := &m.transfers
	m.moveListSelection(&st.selected, &st.scroll, len(st.rows), transfersPanelRows, delta)
	if st.selected >= 0 && st.selected < len(st.rows) {
		st.selectedID = st.rows[st.selected].ID
	}
}

// TransfersSelect puts the panel's cursor on row idx.
func (m *OS) TransfersSelect(idx int) {
	st := &m.transfers
	if idx >= 0 && idx < len(st.rows) {
		st.selected = idx
		st.selectedID = st.rows[idx].ID
	}
}

// TransfersPause pauses the copy under the cursor, or goes on with it when it
// is paused or waits for its machine.
func (m *OS) TransfersPause() tea.Cmd {
	row, ok := m.selectedTransfer()
	if !ok {
		return nil
	}
	switch row.State {
	case "paused", "waiting":
		return m.transferCallCmd("transfer-resume", row.Name, map[string]any{"id": row.ID})
	case "queued", "running", "verifying", "conflict":
		return m.transferCallCmd("transfer-pause", row.Name, map[string]any{"id": row.ID})
	}
	text := "The copy of " + printableTitle(row.Name) + " ended."
	if row.State == "failed" {
		if press := m.transfersPressTo(config.ActionTransfersRetry, "try it again"); press != "" {
			text += " " + press
		}
	}
	m.ShowNotification(text, "info", m.Settings.NotificationDuration)
	return nil
}

// TransfersRetry tries a failed copy again from where it stopped.
func (m *OS) TransfersRetry() tea.Cmd {
	row, ok := m.selectedTransfer()
	if !ok {
		return nil
	}
	if row.State != "failed" {
		m.ShowNotification("Only a failed copy can be tried again.", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.transferCallCmd("transfer-resume", row.Name, map[string]any{"id": row.ID})
}

// TransfersCancel asks before it cancels the copy under the cursor.
func (m *OS) TransfersCancel() {
	row, ok := m.selectedTransfer()
	if !ok {
		return
	}
	if transferEnded(row.State) {
		text := "The copy of " + printableTitle(row.Name) + " ended."
		if press := m.transfersPressTo(config.ActionTransfersClear, "clear it"); press != "" {
			text += " " + press
		}
		m.ShowNotification(text, "info", m.Settings.NotificationDuration)
		return
	}
	m.openCopyDialog(&copyDialog{kind: copyDialogCancel, row: row})
}

// TransfersClear removes the copies that ended from the list, here and in the
// daemon.
func (m *OS) TransfersClear() tea.Cmd {
	var ids []string
	for _, r := range m.transfers.rows {
		if transferEnded(r.State) {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		m.ShowNotification("No copy has ended. Nothing to clear.", "info", m.Settings.NotificationDuration)
		return nil
	}
	call := m.transferCaller()
	return func() tea.Msg {
		_, err := call("transfer-clear", map[string]any{"ids": ids}, transferCallTimeout)
		return transfersClearedMsg{IDs: ids, Err: err}
	}
}

// applyTransfersCleared drops the cleared copies from the mirror.
func (m *OS) applyTransfersCleared(msg transfersClearedMsg) {
	if msg.Err != nil {
		m.ShowNotification(transferCallFailed(transferCallMsg{Verb: "transfer-clear", Err: msg.Err}), "error", m.Settings.NotificationDuration*2)
		return
	}
	gone := map[string]bool{}
	for _, id := range msg.IDs {
		gone[id] = true
	}
	m.transfers.rows = slices.DeleteFunc(m.transfers.rows, func(r session.TransferRow) bool { return gone[r.ID] })
	m.transfersChanged()
}

// TransfersAnswer opens the question for a copy that waits for an answer.
func (m *OS) TransfersAnswer() {
	row, ok := m.selectedTransfer()
	if !ok {
		return
	}
	if row.State != "conflict" || len(row.Conflicts) == 0 {
		m.ShowNotification("The copy of "+printableTitle(row.Name)+" does not wait for an answer.", "info", m.Settings.NotificationDuration)
		return
	}
	m.openCopyDialog(&copyDialog{kind: copyDialogConflict, row: row, rows: copyConflictChoices})
}

// transfersHostMark is the progress of the copies that have a host at one
// end, bytes weighted, and whether any does: the rail's machine row shows it.
func (m *OS) transfersHostMark(host string) (pct int, ok bool) {
	if host == "" || host == federation.LocalHostName || !m.transfersShown() {
		return 0, false
	}
	var size, done int64
	for _, r := range m.transfers.rows {
		if transferEnded(r.State) || r.Src.Host != host && r.Dst.Host != host {
			continue
		}
		ok = true
		size += r.Size
		done += min(r.Done, r.Size)
	}
	if !ok {
		return 0, false
	}
	return transferPercent(done, size), true
}

// transferPercent is done of size in whole percent, 99 until it is all done.
func transferPercent(done, size int64) int {
	if size <= 0 {
		return 0
	}
	if done >= size {
		return 100
	}
	return min(int(done*100/size), 99)
}
