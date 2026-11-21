package app

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/crashexit"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

const transferQueue = 16

const transferBatch = 100 * time.Millisecond

const transferLinger = 10 * time.Second

const transferRetryMax = 30 * time.Second

const transferCallTimeout = 15 * time.Second

type transfersState struct {
	rows        []session.TransferRow
	live        bool
	unsupported bool
	seen        map[string]bool
	lastEnd     time.Time
	lingerGen   uint64

	events chan tea.Msg
	stop   func()

	open       bool
	list       listnav.State
	selectedID string

	dialog *copyDialog

	drops []*pendingDrop

	call inboxVerbCall
}

type transferEvent struct {
	Type   string
	Action string
	Row    session.TransferRow
}

type transfersSnapshotMsg struct {
	Rows  []session.TransferRow
	Early []transferEvent
}

type transfersEventsMsg struct {
	Events []transferEvent
}

type transfersDownMsg struct {
	Err         error
	Unsupported bool
}

type transfersWatchMsg struct {
	msg tea.Msg
}

type transfersLingerMsg struct {
	gen uint64
}

type transferCallMsg struct {
	Verb string
	Name string
	Row  *session.TransferRow
	Err  error
}

type transfersClearedMsg struct {
	IDs []string
	Err error
}

var errTransferGap = errors.New("the transfer stream has a gap")

func transferEnded(state string) bool {
	return state == "done" || state == "failed" || state == "cancelled"
}

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

func (m *OS) endTransfersWatch() {
	if m.transfers.stop != nil {
		m.transfers.stop()
		m.transfers.stop = nil
	}
}

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

func runTransfersWatch(ctx context.Context, dial inboxDial, out chan<- tea.Msg) {
	defer crashexit.Recover()
	defer close(out)
	wait := time.Second
	for ctx.Err() == nil {
		listed, err := transfersWatchOnce(ctx, dial, out)
		if ctx.Err() != nil {
			return
		}
		if code := session.VerbErrCode(err); code == session.ErrVerbUnknownVerb || code == session.ErrVerbInvalidParams {
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

func transfersWatchOnce(ctx context.Context, dial inboxDial, out chan<- tea.Msg) (bool, error) {
	client, err := dial()
	if err != nil {
		return false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	defer func() { _ = client.Close() }()

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
			if jsonx.Unmarshal(line, &ev) != nil {
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
	if err := jsonx.Unmarshal(raw, &listing); err != nil {
		return false, err
	}
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

func sortTransferRows(rows []session.TransferRow) {
	sortx.SortStableFunc(rows, func(a, b session.TransferRow) int {
		if c := cmp.Compare(b.Created, a.Created); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

func (m *OS) transferRowIndex(id string) int {
	return slices.IndexFunc(m.transfers.rows, func(r session.TransferRow) bool { return r.ID == id })
}

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

func transferRemovedEvent(ev transferEvent) bool {
	return ev.Type == session.EventTransfer && ev.Action == "removed"
}

func (m *OS) dropTransferRow(id string) {
	m.transfers.rows = slices.DeleteFunc(m.transfers.rows, func(r session.TransferRow) bool { return r.ID == id })
}

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
	if cmd, taken := m.farOpenCopyEnded(row); taken {
		return cmd
	}
	if cmd, taken := m.dropCopyEnded(row); taken {
		return cmd
	}
	m.transferEndNotice(row)
	return nil
}

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
	return m.tickUntilStop(left+50*time.Millisecond, func(time.Time) tea.Msg { return transfersLingerMsg{gen: gen} })
}

func (m *OS) applyTransfersLinger(msg transfersLingerMsg) {
	if msg.gen != m.transfers.lingerGen {
		return
	}
	m.transfersChanged()
}

func (m *OS) transfersChanged() {
	m.clampTransfersSelection()
	m.sidebarCache.invalidate()
	m.secondarySidebarCache.invalidate()
	m.cachedViewContent = ""
	m.renderSkipped = false
}

func (m *OS) transfersActive() int {
	n := 0
	for _, r := range m.transfers.rows {
		if !transferEnded(r.State) {
			n++
		}
	}
	return n
}

func (m *OS) transfersShown() bool {
	st := &m.transfers
	for _, r := range st.rows {
		if !transferEnded(r.State) || r.State == "failed" && !st.seen[r.ID] {
			return true
		}
	}
	return !st.lastEnd.IsZero() && time.Since(st.lastEnd) < transferLinger
}

func (m *OS) markTransferSeen(id string) {
	if m.transfers.seen == nil {
		m.transfers.seen = map[string]bool{}
	}
	m.transfers.seen[id] = true
}

func transferPlace(host string) string {
	if host == "" || host == federation.LocalHostName {
		return thisMachineLabel()
	}
	return printableTitle(host)
}

func transferPlaceShort(host string) string {
	if host == "" || host == federation.LocalHostName {
		return thisMachineShort()
	}
	return printableTitle(host)
}

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
		if row.Denied > 0 {
			were := " were"
			if row.Denied == 1 {
				were = " was"
			}
			text += " " + countOf(row.Denied, "item") + were + " not copied. A linked machine may not read keys or credentials."
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

func transferFailedText(row session.TransferRow) string {
	text := "The copy of " + printableTitle(row.Name) + " to " + transferPlace(row.Dst.Host) + " stopped"
	if why := strings.TrimSpace(printableTitle(row.Error)); why != "" {
		text += ": " + strings.TrimSuffix(why, ".")
	}
	return text + "."
}

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

func (m *OS) transferCaller() inboxVerbCall {
	if m.transfers.call != nil {
		return m.transfers.call
	}
	return m.inboxCaller()
}

func (m *OS) transferCallCmd(verb, name string, params map[string]any) tea.Cmd {
	call := m.transferCaller()
	return func() tea.Msg {
		raw, err := call(verb, params, transferCallTimeout)
		msg := transferCallMsg{Verb: verb, Name: name, Err: err}
		if err == nil {
			var row session.TransferRow
			if jsonx.Unmarshal(raw, &row) == nil && row.ID != "" {
				msg.Row = &row
			}
		}
		return msg
	}
}

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

func (m *OS) clampTransfersSelection() {
	st := &m.transfers
	if st.selectedID != "" {
		if i := m.transferRowIndex(st.selectedID); i >= 0 {
			st.list.Cursor = i
			return
		}
	}
	st.list.Cursor = clampInt(st.list.Cursor, 0, max(len(st.rows)-1, 0))
	if st.list.Cursor < len(st.rows) {
		st.selectedID = st.rows[st.list.Cursor].ID
	}
}

func (m *OS) selectedTransfer() (session.TransferRow, bool) {
	st := &m.transfers
	if st.list.Cursor < 0 || st.list.Cursor >= len(st.rows) {
		return session.TransferRow{}, false
	}
	return st.rows[st.list.Cursor], true
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
	st.list.Cursor, st.list.Scroll, st.selectedID = 0, 0, ""
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
	st.list.Move(delta, len(st.rows), transfersPanelRows, m.listWraps(), nil)
	if st.list.Cursor >= 0 && st.list.Cursor < len(st.rows) {
		st.selectedID = st.rows[st.list.Cursor].ID
	}
}

// TransfersSelect puts the panel's cursor on row idx.
func (m *OS) TransfersSelect(idx int) {
	st := &m.transfers
	if idx >= 0 && idx < len(st.rows) {
		st.list.Cursor = idx
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

func transferPercent(done, size int64) int {
	if size <= 0 {
		return 0
	}
	if done >= size {
		return 100
	}
	return min(int(done*100/size), 99)
}
