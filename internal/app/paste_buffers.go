package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Paste buffers: the yanks tuios keeps to paste again, after tmux's.
//
// Each yank in copy mode, and each mouse selection copied to the clipboard,
// is also kept as a paste buffer. The clipboard write is unchanged. The
// paste_buffer action (prefix ]) pastes the newest buffer into the focused
// pane, and choose_buffer (prefix #) lists them to pick one.
//
// A client with a daemon keeps the buffers there (session/verb_buffers.go),
// so every client and session shares them and `tuios list-buffers` sees
// them. The calls run off the update loop. A client with no daemon keeps its
// own store, which ends with the client.

// overlayKindBuffers is the buffer chooser's overlay kind.
const overlayKindBuffers = "buffers"

// pasteBufferTimeout bounds one buffer call to the daemon.
const pasteBufferTimeout = 5 * time.Second

// PasteBufferItem is one row of the buffer chooser.
type PasteBufferItem struct {
	Name   string `json:"name"`
	Bytes  int    `json:"bytes"`
	Sample string `json:"sample"`
	// Version is the content the row shows. Enter and d act only while the
	// buffer still holds it.
	Version uint64 `json:"version"`
	// Pane and Session say a process in a pane set the buffer, and where.
	// The chooser marks such a row: the person did not copy that text.
	Pane    string `json:"pane"`
	Session string `json:"session"`
}

// bufferChooser is the state of the buffer chooser overlay.
type bufferChooser struct {
	open     bool
	items    []PasteBufferItem
	selected int
	scroll   int
	loading  bool
	err      string
	gen      uint64
	// target is the pane focused when the chooser opened, which its paste
	// goes to.
	target string
}

// PasteBuffersLoadedMsg carries the buffer list for the chooser.
type PasteBuffersLoadedMsg struct {
	Gen   uint64
	Items []PasteBufferItem
	Err   error
}

// PasteBufferFetchedMsg carries one buffer's text, to paste into the pane
// Window names: the pane that was focused when the key was pressed.
type PasteBufferFetchedMsg struct {
	Name   string
	Data   string
	Window string
	Err    error
	// asked and version are what the paste asked for, so a fallback to the
	// local store asks the same.
	asked   string
	version uint64
}

// PasteBufferDeletedMsg reports a delete from the chooser.
type PasteBufferDeletedMsg struct {
	Name string
	Err  error
}

// PasteBufferSaveFailedMsg reports a yank the daemon did not keep.
type PasteBufferSaveFailedMsg struct {
	Text string
	Err  error
}

// bufferChooserRows is the requested row count of the chooser.
const (
	bufferChooserWidth = 64
	bufferChooserRows  = 10
)

// localBuffers is the store of a client with no daemon, or with a daemon
// from before the buffer verbs, with the limits of the config as it stands
// now.
func (m *OS) localBuffers() *pastebuf.Store {
	limit, maxBytes := pastebuf.DefaultLimit, pastebuf.DefaultMaxBytes
	if m.UserConfig != nil {
		limit, maxBytes = m.UserConfig.PasteBuffers.Resolved()
	}
	if m.pasteBufs == nil {
		m.pasteBufs = pastebuf.New(limit, maxBytes)
	} else {
		m.pasteBufs.SetLimits(limit, maxBytes)
	}
	return m.pasteBufs
}

// buffersInDaemon reports whether this client keeps its buffers in the
// daemon: it has one, and the daemon has the buffer verbs.
func (m *OS) buffersInDaemon() bool {
	return m.IsDaemonSession && !m.buffersDaemonOld
}

// isUnknownVerb reports whether err is a daemon's unknown_verb: a daemon from
// before the buffer verbs.
func isUnknownVerb(err error) bool {
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == "unknown_verb"
}

// noteOldDaemon switches this client to its own store, for a daemon that
// answered unknown_verb. It says so once.
func (m *OS) noteOldDaemon() {
	if m.buffersDaemonOld {
		return
	}
	m.buffersDaemonOld = true
	m.LogInfo("The daemon has no paste buffers. This client keeps its own until the daemon restarts on this version.")
}

// bufferCall makes one buffer verb call to this machine's daemon.
func (m *OS) bufferCall() func(verb string, params map[string]any) (json.RawMessage, error) {
	call := m.inboxCaller()
	return func(verb string, params map[string]any) (json.RawMessage, error) {
		return call(verb, params, pasteBufferTimeout)
	}
}

// SaveToPasteBuffers keeps text as a new paste buffer. It returns the call
// to the daemon to run, or nil when the client keeps its own store. A failed
// save is logged and not shown: the clipboard write, which the person asked
// for, already happened.
func (m *OS) SaveToPasteBuffers(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	if !m.buffersInDaemon() {
		m.saveLocalBuffer(text)
		return nil
	}
	call := m.bufferCall()
	params := map[string]any{"data": text}
	if m.SessionName != "" && m.AttachedHost == "" {
		// The session the yank was made in, so a pane of it may read it.
		params["session"] = m.SessionName
	}
	return func() tea.Msg {
		if _, err := call("set-buffer", params); err != nil {
			return PasteBufferSaveFailedMsg{Text: text, Err: err}
		}
		return nil
	}
}

// saveLocalBuffer keeps text in this client's own store.
func (m *OS) saveLocalBuffer(text string) {
	if _, err := m.localBuffers().Add(text, pastebuf.Owner{}); err != nil && !errors.Is(err, pastebuf.ErrOff) {
		m.LogInfo("Paste buffer not kept: %v", err)
	}
}

// handlePasteBufferSaveFailed logs a yank the daemon did not keep, and keeps
// it here when the daemon is too old to keep it.
func (m *OS) handlePasteBufferSaveFailed(msg PasteBufferSaveFailedMsg) {
	if isUnknownVerb(msg.Err) {
		m.noteOldDaemon()
		m.saveLocalBuffer(msg.Text)
		return
	}
	m.LogInfo("Paste buffer not kept: %v", msg.Err)
}

// PasteNewestBuffer is the paste_buffer action: paste the newest buffer into
// the focused pane. The newest is the newest the person copied, or one a pane
// of this session set: a pane of another session cannot plant what this key
// pastes.
func (m *OS) PasteNewestBuffer() tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil {
		m.ShowNotification("No pane to paste into", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.pasteBufferNamed("", w.ID, 0)
}

// pasteBufferNamed pastes the buffer called name, or the newest for "", into
// the pane with the id window. The id is taken when the key is pressed, so a
// pane that takes focus while the text is read does not get the paste. A
// nonzero version pastes the buffer only while it holds the content the
// chooser listed.
func (m *OS) pasteBufferNamed(name, window string, version uint64) tea.Cmd {
	if !m.buffersInDaemon() {
		b, err := m.localBuffers().Get(name, version, nil)
		return m.handlePasteBufferFetched(PasteBufferFetchedMsg{Name: b.Name, Data: b.Data, Window: window, Err: err, asked: name, version: version})
	}
	call := m.bufferCall()
	params := map[string]any{"encoding": "base64"}
	if name != "" {
		params["name"] = name
	} else {
		params["for_session"] = m.pasteSession()
	}
	if version != 0 {
		params["version"] = version
	}
	return func() tea.Msg {
		raw, err := call("show-buffer", params)
		if err != nil {
			return PasteBufferFetchedMsg{Name: name, Window: window, Err: err, asked: name, version: version}
		}
		var res struct {
			Name    string `json:"name"`
			Data    string `json:"data"`
			DataB64 string `json:"data_b64"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return PasteBufferFetchedMsg{Name: name, Window: window, Err: err, asked: name}
		}
		data := res.Data
		if res.DataB64 != "" {
			// Every byte, where an older daemon gives text alone.
			if b, err := base64.StdEncoding.DecodeString(res.DataB64); err == nil {
				data = string(b)
			}
		}
		return PasteBufferFetchedMsg{Name: res.Name, Data: data, Window: window}
	}
}

// pasteSession is the session the paste key takes buffers for: this
// client's session on this machine. A session on another machine has no
// buffers here, so only the person's own count there.
func (m *OS) pasteSession() string {
	if m.AttachedHost != "" {
		return ""
	}
	return m.SessionName
}

// handlePasteBufferFetched pastes a fetched buffer into the pane it was asked
// for.
func (m *OS) handlePasteBufferFetched(msg PasteBufferFetchedMsg) tea.Cmd {
	d := m.Settings.NotificationDuration
	if msg.Err != nil {
		if isUnknownVerb(msg.Err) && m.buffersInDaemon() {
			m.noteOldDaemon()
			return m.pasteBufferNamed(msg.asked, msg.Window, msg.version)
		}
		if isChangedErr(msg.Err) {
			m.ShowNotification("Buffer "+msg.Name+" changed after the list showed it. Nothing was pasted.", "warning", d)
			return nil
		}
		if isNoBufferErr(msg.Err) {
			m.ShowNotification("There are no paste buffers. A yank in copy mode adds one.", "info", d)
			return nil
		}
		m.ShowNotification("Could not read the paste buffer: "+msg.Err.Error(), "error", d)
		return nil
	}
	w := m.windowByID(msg.Window)
	if w == nil {
		m.ShowNotification("The pane to paste into is gone", "warning", d)
		return nil
	}
	if w.CopyModeVisible() && !w.InImplicitCopyMode() {
		m.ShowNotification("Cannot paste in copy mode. Exit copy mode first.", "warning", d)
		return nil
	}
	// A line feed becomes a carriage return, as tmux's paste-buffer does.
	if !m.PasteIntoWindow(w, pastebuf.PasteText(msg.Data, false)) {
		m.ShowNotification("Paste failed", "error", d)
		return nil
	}
	m.ShowNotification(fmt.Sprintf("Pasted %s (%d chars)", msg.Name, len(msg.Data)), "success", d)
	return nil
}

// isChangedErr reports whether err says the buffer was set again after the
// list showed it, from the daemon or the local store.
func isChangedErr(err error) bool {
	return errors.Is(err, pastebuf.ErrChanged) || strings.Contains(err.Error(), "set again")
}

// isNoBufferErr reports whether err says there is no such buffer, from the
// daemon or the local store.
func isNoBufferErr(err error) bool {
	if errors.Is(err, pastebuf.ErrNone) || errors.Is(err, pastebuf.ErrNotFound) {
		return true
	}
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == "no_buffer"
}

// PasteIntoFocused pastes text into the focused pane, and into the panes
// multifocus types into with it. It reports whether the focused pane took it.
func (m *OS) PasteIntoFocused(text string) bool {
	w := m.GetFocusedWindow()
	if w == nil {
		return false
	}
	return m.PasteIntoWindow(w, text)
}

// PasteIntoWindow pastes text into w, and, when w is the focused pane, into
// the panes multifocus types into with it. It reports whether w took it.
//
// A scroll gesture leaves the pane in an implicit copy mode. A paste, like a
// typed key, means the reading is over: the pane snaps back to live output
// and the paste goes through, to this pane and to the set.
//
// Real copy mode, plain or multi, takes keys as motions. A paste is not a
// motion, and it must not reach the shell under the copy-mode view: in multi
// copy mode every pane of the set is in copy mode, and a paste would land in
// all of their shells unseen. So the paste is dropped.
//
// Each pane gets the text through Window.Paste, which drops control
// characters, so text holding ESC[201~ cannot end the bracketed paste early
// and have the rest run as typed input.
func (m *OS) PasteIntoWindow(w *terminal.Window, text string) bool {
	if w.InImplicitCopyMode() {
		w.ExitCopyMode()
	}
	if w.InCopyMode() {
		return false
	}
	ok := w.Paste(text) == nil
	if w == m.GetFocusedWindow() {
		for _, peer := range m.MultifocusPeers() {
			_ = peer.Paste(text)
		}
	}
	return ok
}

// OpenBufferChooser is the choose_buffer action: list the paste buffers to
// pick one to paste into the focused pane.
func (m *OS) OpenBufferChooser() tea.Cmd {
	target := ""
	if w := m.GetFocusedWindow(); w != nil {
		target = w.ID
	}
	m.buffers = bufferChooser{open: true, gen: m.buffers.gen + 1, loading: true, target: target}
	if !m.buffersInDaemon() {
		m.handlePasteBuffersLoaded(PasteBuffersLoadedMsg{Gen: m.buffers.gen, Items: bufferItems(m.localBuffers().List(nil))})
		return nil
	}
	call, gen := m.bufferCall(), m.buffers.gen
	return func() tea.Msg {
		raw, err := call("list-buffers", map[string]any{})
		if err != nil {
			return PasteBuffersLoadedMsg{Gen: gen, Err: err}
		}
		var res struct {
			Buffers []PasteBufferItem `json:"buffers"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return PasteBuffersLoadedMsg{Gen: gen, Err: err}
		}
		return PasteBuffersLoadedMsg{Gen: gen, Items: res.Buffers}
	}
}

// bufferItems is the chooser's rows for a local store's buffers.
func bufferItems(list []pastebuf.Buffer) []PasteBufferItem {
	out := make([]PasteBufferItem, 0, len(list))
	for _, b := range list {
		out = append(out, PasteBufferItem{Name: b.Name, Bytes: len(b.Data), Sample: pastebuf.Sample(b.Data, 60), Version: b.Version})
	}
	return out
}

// handlePasteBuffersLoaded fills the chooser.
func (m *OS) handlePasteBuffersLoaded(msg PasteBuffersLoadedMsg) {
	if !m.buffers.open || msg.Gen != m.buffers.gen {
		return
	}
	if msg.Err != nil && isUnknownVerb(msg.Err) && m.buffersInDaemon() {
		m.noteOldDaemon()
		msg = PasteBuffersLoadedMsg{Gen: msg.Gen, Items: bufferItems(m.localBuffers().List(nil))}
	}
	m.buffers.loading = false
	m.buffers.err = ""
	if msg.Err != nil {
		m.buffers.err = msg.Err.Error()
		return
	}
	m.buffers.items = msg.Items
	m.buffers.selected = clampInt(m.buffers.selected, 0, max(len(msg.Items)-1, 0))
}

// BufferChooserOpen reports whether the buffer chooser is up.
func (m *OS) BufferChooserOpen() bool { return m.buffers.open }

// CloseBufferChooser hides the buffer chooser.
func (m *OS) CloseBufferChooser() {
	m.buffers = bufferChooser{gen: m.buffers.gen}
}

// BufferChooserSelected is the chooser's selected row.
func (m *OS) BufferChooserSelected() int { return m.buffers.selected }

// BufferChooserItems is the chooser's rows.
func (m *OS) BufferChooserItems() []PasteBufferItem { return m.buffers.items }

// BufferChooserMove steps the chooser's selection.
func (m *OS) BufferChooserMove(delta int) {
	m.moveListSelection(&m.buffers.selected, &m.buffers.scroll, len(m.buffers.items), bufferChooserRows, delta)
}

// BufferChooserSelect puts the chooser's selection on row idx.
func (m *OS) BufferChooserSelect(idx int) {
	if idx >= 0 && idx < len(m.buffers.items) {
		m.buffers.selected = idx
	}
}

// BufferChooserActivate pastes the buffer on row idx and closes the chooser.
func (m *OS) BufferChooserActivate(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.buffers.items) {
		return nil
	}
	name, target, version := m.buffers.items[idx].Name, m.buffers.target, m.buffers.items[idx].Version
	m.CloseBufferChooser()
	if target == "" {
		m.ShowNotification("No pane to paste into", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.pasteBufferNamed(name, target, version)
}

// BufferChooserDelete deletes the buffer on the selected row.
func (m *OS) BufferChooserDelete() tea.Cmd {
	idx := m.buffers.selected
	if idx < 0 || idx >= len(m.buffers.items) {
		return nil
	}
	// Only the content the row shows goes: a buffer set again since the
	// list loaded stays.
	name, version := m.buffers.items[idx].Name, m.buffers.items[idx].Version
	m.buffers.items = append(m.buffers.items[:idx:idx], m.buffers.items[idx+1:]...)
	m.buffers.selected = clampInt(idx, 0, max(len(m.buffers.items)-1, 0))
	if !m.buffersInDaemon() {
		_, err := m.localBuffers().Delete(name, version, nil)
		m.handlePasteBufferDeleted(PasteBufferDeletedMsg{Name: name, Err: err})
		return nil
	}
	call := m.bufferCall()
	return func() tea.Msg {
		_, err := call("delete-buffer", map[string]any{"name": name, "version": version})
		return PasteBufferDeletedMsg{Name: name, Err: err}
	}
}

// handlePasteBufferDeleted says how a delete ended.
func (m *OS) handlePasteBufferDeleted(msg PasteBufferDeletedMsg) {
	d := m.Settings.NotificationDuration
	if msg.Err != nil && isChangedErr(msg.Err) {
		m.ShowNotification("Buffer "+msg.Name+" changed after the list showed it. Nothing was deleted.", "warning", d)
		return
	}
	if msg.Err != nil && !isNoBufferErr(msg.Err) {
		m.ShowNotification("Could not delete "+msg.Name+": "+msg.Err.Error(), "error", d)
		return
	}
	m.ShowNotification("Deleted "+msg.Name, "info", d)
}

// renderBufferChooser draws the chooser on the shared list overlay.
func (m *OS) renderBufferChooser() (string, overlay.Geometry, []overlayRowHit) {
	items := m.buffers.items
	if len(items) > 0 {
		m.buffers.selected = clampInt(m.buffers.selected, 0, len(items)-1)
	}
	empty := "No paste buffers. A yank in copy mode adds one."
	if m.buffers.err != "" {
		empty = "Could not read the paste buffers: " + m.buffers.err
	}
	return m.renderListOverlay(listOverlay{
		Title:      "Paste buffers",
		Width:      bufferChooserWidth,
		MaxVisible: bufferChooserRows,
		Count:      len(items),
		Selected:   m.buffers.selected,
		Scroll:     &m.buffers.scroll,
		EmptyMsg:   empty,
		Pending:    m.buffers.loading,
		Hints: []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "paste"},
			{Key: "d", Label: "delete"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return bufferChooserRow(items[i], selected, rowBg, pal, width)
		},
	})
}

// bufferChooserRow draws one buffer: its name, the start of its text, and its
// size. A buffer a process in a pane set says so before its size, since the
// person did not copy that text.
func bufferChooserRow(b PasteBufferItem, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(byteSize(b.Bytes))
	tag := ""
	if b.Pane != "" {
		tag = "from pane " + b.Pane + "  "
		right = overlay.Style(rowBg).Foreground(pal.Warning).Render(tag) + right
	}
	nameColor, sampleColor := pal.FgMute, pal.FgDim
	if selected {
		nameColor, sampleColor = pal.Accent, pal.Fg
	}
	name := overlay.Truncate(printableTitle(b.Name), 16)
	left := overlay.Style(rowBg).Foreground(nameColor).Bold(true).Render(name)
	room := width - len([]rune(name)) - len(byteSize(b.Bytes)) - len([]rune(tag)) - 8
	if room > 0 {
		left += overlay.Style(rowBg).Foreground(sampleColor).Render("  " + overlay.Truncate(printableTitle(b.Sample), room))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}

// byteSize is a buffer size as a short label.
func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	case n == 1:
		return "1 byte"
	}
	return fmt.Sprintf("%d bytes", n)
}
