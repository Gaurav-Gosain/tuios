package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// scrollbackCaptureTimeout bounds the daemon's capture of a pane's history.
// A full history is a few megabytes at most, so this is slack for a busy
// daemon, not for the size.
const scrollbackCaptureTimeout = 30 * time.Second

// scrollbackEditorFile is the name of the file in the private folder. The
// .txt suffix lets an editor pick plain text for it.
const scrollbackEditorFile = "scrollback.txt"

// scrollbackFileMsg is the scrollback written to its file, or why it was not.
type scrollbackFileMsg struct {
	dir, path string
	// line is the 1-based line of the file the editor opens at, 0 for none.
	line int
	argv []string
	err  error
}

// scrollbackEditorDoneMsg is the editor closing.
type scrollbackEditorDoneMsg struct {
	dir string
	err error
}

// scrollbackView is where the focused pane's view was when the edit started,
// in the client's own copy of the pane.
type scrollbackView struct {
	// line is the 0-based absolute line: the copy-mode cursor's, or the top
	// of the view outside copy mode.
	line int
	// history is how many scrollback lines the client's copy holds. The
	// daemon can hold more, since a client is sent only part of the history
	// when it attaches, so a line is moved by the difference.
	history int
}

// scrollbackEditorReason says why this client cannot open an editor, or ""
// when it can. The editor runs on the terminal tuios runs on, so a person at
// a browser or at a remote SSH client would not see it. These are the same
// clients the draft editor (ctrl+g) refuses.
func (m *OS) scrollbackEditorReason() string {
	switch {
	case m.BrowserClient:
		return "The browser cannot open an editor. Use tuios in a terminal to edit the scrollback."
	case m.RemoteClient && !m.SSHIsLoopback:
		return "tuios runs on another machine. It cannot open an editor on your machine."
	case m.ProcessingRemoteKeys:
		return "Only your own key opens the scrollback in the editor."
	}
	return ""
}

// EditScrollbackInEditor writes the focused pane's whole history and screen
// to a file as plain text and opens it in the editor: the File editor
// setting, then $EDITOR, $VISUAL and vi. The editor takes over this terminal
// until it exits. An editor that takes a line opens at the copy-mode cursor,
// or at the top of the view outside copy mode.
//
// The file is in a private folder (0700, the file 0600) and goes away when
// the editor exits, when something fails on the way, and when the client
// ends. It holds the pane's whole history, so nothing leaves it behind.
//
// In a daemon session the text comes from the daemon. A client holds only
// part of a pane's history after it attaches, and the daemon holds all of it.
func (m *OS) EditScrollbackInEditor() tea.Cmd {
	if reason := m.scrollbackEditorReason(); reason != "" {
		m.ShowNotification(reason, "warning", m.Settings.NotificationDuration)
		return nil
	}
	w := m.GetFocusedWindow()
	if w == nil || w.Terminal == nil {
		m.ShowNotification("No pane is open. Open a pane to edit its scrollback.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	argv, err := editorArgv(m.Settings.SidebarEditor)
	if err != nil {
		m.LogError("Could not read the editor command: %v", err)
		m.ShowNotification(editorCommandNote, "warning", m.Settings.NotificationDuration)
		return nil
	}
	dir, err := os.MkdirTemp("", "tuios-scrollback-")
	if err != nil {
		m.ShowNotification("tuios cannot make a file for the editor: "+err.Error(), "error", m.Settings.NotificationDuration)
		return nil
	}
	// A second edit before the first is done would orphan the first folder,
	// so the older one goes now. The editor holds the terminal while it runs,
	// so this happens only when a capture is slower than the person.
	m.removeScrollbackEditorFile()
	m.scrollbackEditorDir = dir
	path := filepath.Join(dir, scrollbackEditorFile)

	w.RLockIO()
	view := scrollbackViewOf(w)
	w.RUnlockIO()

	if m.IsDaemonSession && m.DaemonClient != nil {
		req := scrollbackCaptureRequest{
			Session: m.SessionName,
			Window:  w.ID,
			Host:    m.AttachedHost,
			Build:   m.DaemonClient.ClientVersion(),
		}
		return func() tea.Msg {
			history, lines, err := captureScrollbackToFile(req, path)
			return scrollbackFileMsg{dir: dir, path: path, line: view.fileLine(history, lines), argv: argv, err: err}
		}
	}

	w.RLockIO()
	history, lines, err := writeScrollbackFile(path, func(out io.Writer) (int, error) {
		return streamWindowText(w, out)
	})
	w.RUnlockIO()
	return m.openScrollbackEditor(scrollbackFileMsg{dir: dir, path: path, line: view.fileLine(history, lines), argv: argv, err: err})
}

// scrollbackViewOf reads where w's view is. The caller holds w's I/O lock.
func scrollbackViewOf(w *terminal.Window) scrollbackView {
	history := w.Terminal.ScrollbackLen()
	v := scrollbackView{history: history, line: history}
	if cm := w.CopyMode; cm != nil && cm.Active {
		v.line = history - cm.ScrollOffset + cm.CursorY
	}
	return v
}

// fileLine maps the view's line to the 1-based line of a file that holds
// history scrollback lines and then the screen, lines lines in all. The
// client's history is the newest part of the file's, so the two line up from
// the screen upwards. 0 means the file is empty.
func (v scrollbackView) fileLine(history, lines int) int {
	if lines <= 0 {
		return 0
	}
	return min(max(v.line+history-v.history+1, 1), lines)
}

// openScrollbackEditor runs the editor on a written file, or reports why the
// file is not there and removes its folder.
func (m *OS) openScrollbackEditor(msg scrollbackFileMsg) tea.Cmd {
	if m.scrollbackEditorDir != msg.dir {
		// The client ended, or a newer edit replaced this one, while the
		// capture ran. The folder is gone or going; open nothing.
		_ = os.RemoveAll(msg.dir)
		return nil
	}
	if msg.err != nil {
		m.dropScrollbackEditorDir(msg.dir)
		m.LogError("Could not write the scrollback for the editor: %v", msg.err)
		m.ShowNotification("tuios cannot write the scrollback to a file: "+msg.err.Error(), "error", m.Settings.NotificationDuration)
		return nil
	}
	args := append(msg.argv[1:len(msg.argv):len(msg.argv)], editorLineArgs(msg.argv[0], msg.path, msg.line)...)
	c := exec.Command(msg.argv[0], args...) //nolint:gosec // the person's own editor
	dir := msg.dir
	// SGR-pixel reports go off first: the editor does not expect them, and
	// the terminal comes back in SGR cells.
	return tea.Sequence(m.releaseHostPixelMouse(), tea.ExecProcess(c, func(err error) tea.Msg {
		return scrollbackEditorDoneMsg{dir: dir, err: err}
	}))
}

// applyScrollbackEditorDone removes the file the editor had open.
func (m *OS) applyScrollbackEditorDone(msg scrollbackEditorDoneMsg) tea.Cmd {
	m.dropScrollbackEditorDir(msg.dir)
	if msg.err != nil {
		m.ShowNotification("The editor did not start or did not stop correctly: "+msg.err.Error(), "warning", m.Settings.NotificationDuration)
	}
	return func() tea.Msg { return hostTerminalBackMsg{} }
}

// dropScrollbackEditorDir removes dir, and forgets it if it is the open one.
func (m *OS) dropScrollbackEditorDir(dir string) {
	_ = os.RemoveAll(dir)
	if m.scrollbackEditorDir == dir {
		m.scrollbackEditorDir = ""
	}
}

// removeScrollbackEditorFile removes the scrollback file, if the client ends
// while the editor has it open.
func (m *OS) removeScrollbackEditorFile() {
	if m.scrollbackEditorDir != "" {
		_ = os.RemoveAll(m.scrollbackEditorDir)
		m.scrollbackEditorDir = ""
	}
}

// editorLineArgs are the arguments after the editor's own: the file, and the
// line to open at when the editor takes one and line is not 0. vi, vim, nvim,
// nano, emacs and kakoune take +LINE before the file. helix takes FILE:LINE.
// Any other editor gets the file alone, since an argument it does not know
// could be read as a second file to open.
func editorLineArgs(editor, path string, line int) []string {
	if line <= 0 {
		return []string{path}
	}
	n := strconv.Itoa(line)
	switch strings.TrimSuffix(filepath.Base(editor), ".exe") {
	case "vi", "vim", "nvim", "view", "nano", "emacs", "emacsclient", "kak":
		return []string{"+" + n, path}
	case "hx", "helix":
		return []string{path + ":" + n}
	}
	return []string{path}
}

// scrollbackCaptureRequest is what the daemon capture needs, copied off the
// model so it can run off the update goroutine.
type scrollbackCaptureRequest struct {
	Session, Window string
	// Host is the machine the session is on, "" for this one. Build is this
	// client's version, for the handshake through the link.
	Host, Build string
}

// captureScrollbackToFile asks the session's daemon for the pane's whole
// history and screen as plain text and writes it to path. It returns how
// many history lines the daemon holds and how many lines the file has.
//
// The capture verb answers with the text in one reply, so the reply is held
// once. It is written out from there without another copy.
func captureScrollbackToFile(req scrollbackCaptureRequest, path string) (int, int, error) {
	c, err := dialSessionDaemon(req.Host, req.Build)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.CallWithTimeout("capture-pane", map[string]any{
		"session": req.Session,
		"window":  req.Window,
		"source":  "recent",
	}, scrollbackCaptureTimeout)
	if err != nil {
		return 0, 0, err
	}
	var res struct {
		Content     string `json:"content"`
		HistoryRows int    `json:"history_rows"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, 0, fmt.Errorf("read the capture: %w", err)
	}
	_, lines, err := writeScrollbackFile(path, func(out io.Writer) (int, error) {
		_, err := io.WriteString(out, vt.StripKittyPlaceholders(res.Content))
		return res.HistoryRows, err
	})
	return res.HistoryRows, lines, err
}

// writeScrollbackFile makes path with mode 0600 and fills it through write,
// which returns the history line count. It returns that count and the number
// of lines the file holds.
func writeScrollbackFile(path string, write func(io.Writer) (int, error)) (int, int, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, 0, err
	}
	lc := &lineCounter{}
	bw := bufio.NewWriterSize(io.MultiWriter(f, lc), 64<<10)
	history, err := write(bw)
	if err == nil {
		err = bw.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return history, lc.lines(), err
}

// lineCounter counts the lines written through it.
type lineCounter struct {
	newlines int
	// open is true when text follows the last newline.
	open bool
}

func (lc *lineCounter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		lc.newlines += bytes.Count(p, []byte{'\n'})
		lc.open = p[len(p)-1] != '\n'
	}
	return len(p), nil
}

func (lc *lineCounter) lines() int {
	if lc.open {
		return lc.newlines + 1
	}
	return lc.newlines
}

// streamWindowText writes w's scrollback, one line at a time, and then its
// screen, as plain text. It never holds the whole history as one string. It
// returns the number of scrollback lines. The caller holds w's I/O lock.
func streamWindowText(w *terminal.Window, out io.Writer) (int, error) {
	history := w.Terminal.ScrollbackLen()
	var err error
	w.Terminal.ScrollbackRows(0, history, func(_ int, line uv.Line) bool {
		text := vt.StripKittyPlaceholders(vt.StripSixelMarkers(line.String()))
		if _, err = io.WriteString(out, text); err == nil {
			_, err = io.WriteString(out, "\n")
		}
		return err == nil
	})
	if err != nil {
		return history, err
	}
	_, err = io.WriteString(out, vt.StripKittyPlaceholders(w.Terminal.String()))
	return history, err
}
