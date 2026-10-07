package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
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
}

// PasteBuffersLoadedMsg carries the buffer list for the chooser.
type PasteBuffersLoadedMsg struct {
	Gen   uint64
	Items []PasteBufferItem
	Err   error
}

// PasteBufferFetchedMsg carries one buffer's text, to paste.
type PasteBufferFetchedMsg struct {
	Name string
	Data string
	Err  error
}

// PasteBufferDeletedMsg reports a delete from the chooser.
type PasteBufferDeletedMsg struct {
	Name string
	Err  error
}

// bufferChooserRows is the requested row count of the chooser.
const (
	bufferChooserWidth = 64
	bufferChooserRows  = 10
)

// localBuffers is the store of a client with no daemon, with the limits of
// the config as it stands now.
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
// daemon.
func (m *OS) buffersInDaemon() bool {
	return m.IsDaemonSession
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
		if _, err := m.localBuffers().Add(text); err != nil && !errors.Is(err, pastebuf.ErrOff) {
			m.LogInfo("Paste buffer not kept: %v", err)
		}
		return nil
	}
	call := m.bufferCall()
	return func() tea.Msg {
		if _, err := call("set-buffer", map[string]any{"data": text}); err != nil {
			return PasteBufferSaveFailedMsg{Err: err}
		}
		return nil
	}
}

// PasteBufferSaveFailedMsg reports a yank the daemon did not keep.
type PasteBufferSaveFailedMsg struct{ Err error }

// PasteNewestBuffer is the paste_buffer action: paste the newest buffer into
// the focused pane.
func (m *OS) PasteNewestBuffer() tea.Cmd {
	return m.pasteBufferNamed("")
}

// pasteBufferNamed pastes the buffer called name, or the newest for "".
func (m *OS) pasteBufferNamed(name string) tea.Cmd {
	if m.GetFocusedWindow() == nil {
		m.ShowNotification("No pane to paste into", "info", m.Settings.NotificationDuration)
		return nil
	}
	if !m.buffersInDaemon() {
		b, err := m.localBuffers().Get(name)
		m.handlePasteBufferFetched(PasteBufferFetchedMsg{Name: b.Name, Data: b.Data, Err: err})
		return nil
	}
	call := m.bufferCall()
	params := map[string]any{}
	if name != "" {
		params["name"] = name
	}
	return func() tea.Msg {
		raw, err := call("show-buffer", params)
		if err != nil {
			return PasteBufferFetchedMsg{Name: name, Err: err}
		}
		var res struct {
			Name string `json:"name"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return PasteBufferFetchedMsg{Name: name, Err: err}
		}
		return PasteBufferFetchedMsg{Name: res.Name, Data: res.Data}
	}
}

// handlePasteBufferFetched pastes a fetched buffer into the focused pane.
func (m *OS) handlePasteBufferFetched(msg PasteBufferFetchedMsg) {
	d := m.Settings.NotificationDuration
	if msg.Err != nil {
		if isNoBufferErr(msg.Err) {
			m.ShowNotification("There are no paste buffers. A yank in copy mode adds one.", "info", d)
			return
		}
		m.ShowNotification("Could not read the paste buffer: "+msg.Err.Error(), "error", d)
		return
	}
	w := m.GetFocusedWindow()
	if w == nil {
		return
	}
	if w.CopyModeVisible() && !w.InImplicitCopyMode() {
		m.ShowNotification("Cannot paste in copy mode. Exit copy mode first.", "warning", d)
		return
	}
	if !m.PasteIntoFocused(msg.Data) {
		m.ShowNotification("Paste failed", "error", d)
		return
	}
	m.ShowNotification(fmt.Sprintf("Pasted %s (%d chars)", msg.Name, len(msg.Data)), "success", d)
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
func (m *OS) PasteIntoFocused(text string) bool {
	w := m.GetFocusedWindow()
	if w == nil {
		return false
	}
	if w.InImplicitCopyMode() {
		w.ExitCopyMode()
	}
	if w.InCopyMode() {
		return false
	}
	ok := w.Paste(text) == nil
	for _, peer := range m.MultifocusPeers() {
		_ = peer.Paste(text)
	}
	return ok
}

// OpenBufferChooser is the choose_buffer action: list the paste buffers to
// pick one to paste.
func (m *OS) OpenBufferChooser() tea.Cmd {
	m.buffers = bufferChooser{open: true, gen: m.buffers.gen + 1, loading: true}
	if !m.buffersInDaemon() {
		m.handlePasteBuffersLoaded(PasteBuffersLoadedMsg{Gen: m.buffers.gen, Items: bufferItems(m.localBuffers().List())})
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
		out = append(out, PasteBufferItem{Name: b.Name, Bytes: len(b.Data), Sample: pastebuf.Sample(b.Data, 60)})
	}
	return out
}

// handlePasteBuffersLoaded fills the chooser.
func (m *OS) handlePasteBuffersLoaded(msg PasteBuffersLoadedMsg) {
	if !m.buffers.open || msg.Gen != m.buffers.gen {
		return
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
	name := m.buffers.items[idx].Name
	m.CloseBufferChooser()
	return m.pasteBufferNamed(name)
}

// BufferChooserDelete deletes the buffer on the selected row.
func (m *OS) BufferChooserDelete() tea.Cmd {
	idx := m.buffers.selected
	if idx < 0 || idx >= len(m.buffers.items) {
		return nil
	}
	name := m.buffers.items[idx].Name
	m.buffers.items = append(m.buffers.items[:idx:idx], m.buffers.items[idx+1:]...)
	m.buffers.selected = clampInt(idx, 0, max(len(m.buffers.items)-1, 0))
	if !m.buffersInDaemon() {
		_, err := m.localBuffers().Delete(name)
		m.handlePasteBufferDeleted(PasteBufferDeletedMsg{Name: name, Err: err})
		return nil
	}
	call := m.bufferCall()
	return func() tea.Msg {
		_, err := call("delete-buffer", map[string]any{"name": name})
		return PasteBufferDeletedMsg{Name: name, Err: err}
	}
}

// handlePasteBufferDeleted says how a delete ended.
func (m *OS) handlePasteBufferDeleted(msg PasteBufferDeletedMsg) {
	d := m.Settings.NotificationDuration
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
// size.
func bufferChooserRow(b PasteBufferItem, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(byteSize(b.Bytes))
	nameColor, sampleColor := pal.FgMute, pal.FgDim
	if selected {
		nameColor, sampleColor = pal.Accent, pal.Fg
	}
	name := overlay.Truncate(printableTitle(b.Name), 16)
	left := overlay.Style(rowBg).Foreground(nameColor).Bold(true).Render(name)
	room := width - len([]rune(name)) - len(byteSize(b.Bytes)) - 8
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
