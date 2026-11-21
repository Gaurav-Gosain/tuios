package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const overlayKindBuffers = "buffers"

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

type bufferChooser struct {
	open    bool
	items   []PasteBufferItem
	list    listnav.State
	loading bool
	err     string
	gen     uint64
	target  string
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
	Name    string
	Data    string
	Window  string
	Err     error
	asked   string
	version uint64
	held    bool
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

const (
	bufferChooserWidth = 64
	bufferChooserRows  = 10
)

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

func (m *OS) buffersInDaemon() bool {
	return m.IsDaemonSession
}

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
	if len(text) > yankPart {
		return m.uploadYank(text)
	}
	call := m.bufferCall()
	params := map[string]any{"data": text}
	return func() tea.Msg {
		if _, err := call("set-buffer", params); err != nil {
			return PasteBufferSaveFailedMsg{Text: text, Err: err}
		}
		return nil
	}
}

const yankPart = 768 << 10

func (m *OS) uploadYank(text string) tea.Cmd {
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() tea.Msg {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return PasteBufferSaveFailedMsg{Text: text, Err: err}
		}
		defer func() { _ = client.Close() }()
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		for rest := text; rest != ""; {
			part := rest[:min(len(rest), yankPart)]
			rest = rest[len(part):]
			params := map[string]any{"data_b64": base64.StdEncoding.EncodeToString([]byte(part)), "upload": id}
			if rest != "" {
				params["more"] = true
			}
			if _, err := client.CallWithTimeout("set-buffer", params, pasteBufferTimeout); err != nil {
				return PasteBufferSaveFailedMsg{Text: text, Err: err}
			}
		}
		return nil
	}
}

func (m *OS) saveLocalBuffer(text string) {
	if _, err := m.localBuffers().Add(text, pastebuf.Owner{}); err != nil && !errors.Is(err, pastebuf.ErrOff) {
		m.LogInfo("Paste buffer not kept: %v", err)
	}
}

func (m *OS) handlePasteBufferSaveFailed(msg PasteBufferSaveFailedMsg) {
	m.LogInfo("Paste buffer not kept: %v", msg.Err)
}

// PasteNewestBuffer is the paste_buffer action: paste the newest buffer into
// the focused pane. The daemon takes the newest of the person's own buffers,
// so a buffer a pane set never becomes what this key pastes.
func (m *OS) PasteNewestBuffer() tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil {
		m.ShowNotification("No pane to paste into", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.pasteBufferNamed("", w.ID, 0)
}

func (m *OS) pasteBufferNamed(name, window string, version uint64) tea.Cmd {
	if !m.buffersInDaemon() {
		b, err := m.localBuffers().Get(name, version, nil)
		return m.handlePasteBufferFetched(PasteBufferFetchedMsg{Name: b.Name, Data: b.Data, Window: window, Err: err, asked: name, version: version})
	}
	call := m.bufferCall()
	params := map[string]any{"encoding": "base64"}
	if name != "" {
		params["name"] = name
	}
	if version != 0 {
		params["version"] = version
	}
	m.pasteHold.pending++
	return func() tea.Msg {
		raw, err := call("show-buffer", params)
		if err != nil {
			return PasteBufferFetchedMsg{Name: name, Window: window, Err: err, asked: name, version: version, held: true}
		}
		var res struct {
			Name    string `json:"name"`
			Data    string `json:"data"`
			DataB64 string `json:"data_b64"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return PasteBufferFetchedMsg{Name: name, Window: window, Err: err, asked: name, held: true}
		}
		data := res.Data
		if res.DataB64 != "" {
			if b, err := base64.StdEncoding.DecodeString(res.DataB64); err == nil {
				data = string(b)
			}
		}
		return PasteBufferFetchedMsg{Name: res.Name, Data: data, Window: window, held: true}
	}
}

type pasteHold struct {
	pending int
	keys    []tea.Msg
}

func (m *OS) holdKeyForPaste(msg tea.Msg) bool {
	if m.pasteHold.pending == 0 {
		return false
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.KeyReleaseMsg, tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		m.pasteHold.keys = append(m.pasteHold.keys, msg)
		return true
	}
	return false
}

func (m *OS) releasePasteHold() tea.Cmd {
	if m.pasteHold.pending > 0 {
		m.pasteHold.pending--
	}
	handler := getInputHandler()
	var cmds []tea.Cmd
	for m.pasteHold.pending == 0 && len(m.pasteHold.keys) > 0 {
		msg := m.pasteHold.keys[0]
		m.pasteHold.keys = m.pasteHold.keys[1:]
		if handler == nil {
			continue
		}
		_, cmd := handler(msg, m)
		cmds = append(cmds, cmd)
	}
	if len(m.pasteHold.keys) == 0 {
		m.pasteHold.keys = nil
	}
	return tea.Batch(cmds...)
}

func (m *OS) handlePasteBufferFetched(msg PasteBufferFetchedMsg) tea.Cmd {
	d := m.Settings.NotificationDuration
	if msg.Err != nil {
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
	if !m.PasteIntoWindow(w, pastebuf.PasteText(msg.Data, false)) {
		m.ShowNotification("Paste failed", "error", d)
		return nil
	}
	m.ShowNotification(fmt.Sprintf("Pasted %s (%d chars)", msg.Name, len(msg.Data)), "success", d)
	return nil
}

func isChangedErr(err error) bool {
	return errors.Is(err, pastebuf.ErrChanged) || strings.Contains(err.Error(), "set again")
}

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
	if m.DrawsTML(w) {
		if !m.TMLPaste(w, text) {
			m.ShowNotification("Select a text field in the surface first.", "warning", m.Settings.NotificationDuration)
			return false
		}
		return true
	}
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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return PasteBuffersLoadedMsg{Gen: gen, Err: err}
		}
		return PasteBuffersLoadedMsg{Gen: gen, Items: res.Buffers}
	}
}

func bufferItems(list []pastebuf.Buffer) []PasteBufferItem {
	out := make([]PasteBufferItem, 0, len(list))
	for _, b := range list {
		out = append(out, PasteBufferItem{Name: b.Name, Bytes: len(b.Data), Sample: pastebuf.Sample(b.Data, 60), Version: b.Version})
	}
	return out
}

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
	m.buffers.list.Cursor = clampInt(m.buffers.list.Cursor, 0, max(len(msg.Items)-1, 0))
}

// BufferChooserOpen reports whether the buffer chooser is up.
func (m *OS) BufferChooserOpen() bool { return m.buffers.open }

// CloseBufferChooser hides the buffer chooser.
func (m *OS) CloseBufferChooser() {
	m.buffers = bufferChooser{gen: m.buffers.gen}
}

// BufferChooserSelected is the chooser's selected row.
func (m *OS) BufferChooserSelected() int { return m.buffers.list.Cursor }

// BufferChooserMove steps the chooser's selection.
func (m *OS) BufferChooserMove(delta int) {
	m.buffers.list.Move(delta, len(m.buffers.items), bufferChooserRows, m.listWraps(), nil)
}

// BufferChooserSelect puts the chooser's selection on row idx.
func (m *OS) BufferChooserSelect(idx int) {
	if idx >= 0 && idx < len(m.buffers.items) {
		m.buffers.list.Cursor = idx
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
	idx := m.buffers.list.Cursor
	if idx < 0 || idx >= len(m.buffers.items) {
		return nil
	}
	name, version := m.buffers.items[idx].Name, m.buffers.items[idx].Version
	m.buffers.items = append(m.buffers.items[:idx:idx], m.buffers.items[idx+1:]...)
	m.buffers.list.Cursor = clampInt(idx, 0, max(len(m.buffers.items)-1, 0))
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

func (m *OS) renderBufferChooser() (string, overlay.Geometry, []overlayRowHit) {
	items := m.buffers.items
	if len(items) > 0 {
		m.buffers.list.Cursor = clampInt(m.buffers.list.Cursor, 0, len(items)-1)
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
		Selected:   m.buffers.list.Cursor,
		Scroll:     &m.buffers.list.Scroll,
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

func bufferChooserRow(b PasteBufferItem, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	size := units.Bytes(int64(b.Bytes))
	nameColor, sampleColor := pal.FgMute, pal.FgDim
	if selected {
		nameColor, sampleColor = pal.Accent, pal.Fg
	}
	name := overlay.Truncate(printableTitle(b.Name), 16)
	fixed := 8 + ansi.StringWidth(name) + ansi.StringWidth(size)
	tag := ""
	if b.Pane != "" {
		tag = overlay.Truncate("from pane "+paneTagTitle(b.Pane), bufferPaneTagCells)
		if width-fixed-ansi.StringWidth(tag)-2 < bufferSampleMinCells {
			tag = "pane"
		}
	}
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(size)
	if tag != "" {
		right = overlay.Style(rowBg).Foreground(pal.Warning).Render(tag) + overlay.Style(rowBg).Render("  ") + right
		fixed += ansi.StringWidth(tag) + 2
	}
	left := overlay.Style(rowBg).Foreground(nameColor).Bold(true).Render(name)
	if room := width - fixed; room > 0 {
		left += overlay.Style(rowBg).Foreground(sampleColor).Render("  " + overlay.Truncate(printableTitle(b.Sample), room))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}

const (
	bufferPaneTagCells   = 20
	bufferSampleMinCells = 12
)

func paneTagTitle(title string) string {
	return strings.Join(strings.Fields(printableTitle(title)), " ")
}
