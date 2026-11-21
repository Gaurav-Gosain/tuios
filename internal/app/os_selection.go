package app

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

const scrollbackCaptureTimeout = 30 * time.Second

const scrollbackEditorFile = "scrollback.txt"

type scrollbackFileMsg struct {
	dir, path string
	line      int
	argv      []string
	err       error
}

type scrollbackEditorDoneMsg struct {
	dir string
	err error
}

type scrollbackView struct {
	line    int
	history int
}

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

func scrollbackViewOf(w *terminal.Window) scrollbackView {
	history := w.Terminal.ScrollbackLen()
	v := scrollbackView{history: history, line: history}
	if cm := w.CopyMode; cm != nil && cm.Active {
		v.line = history - cm.ScrollOffset + cm.CursorY
	}
	return v
}

func (v scrollbackView) fileLine(history, lines int) int {
	if lines <= 0 {
		return 0
	}
	return min(max(v.line+history-v.history+1, 1), lines)
}

func (m *OS) openScrollbackEditor(msg scrollbackFileMsg) tea.Cmd {
	if m.scrollbackEditorDir != msg.dir {
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
	c := m.editorCommand(exec.Command(msg.argv[0], args...)) //nolint:gosec // the person's own editor
	dir := msg.dir
	return tea.Sequence(m.releaseHostPixelMouse(), m.execProcess(c, func(err error) tea.Msg {
		return scrollbackEditorDoneMsg{dir: dir, err: err}
	}))
}

func (m *OS) applyScrollbackEditorDone(msg scrollbackEditorDoneMsg) tea.Cmd {
	m.dropScrollbackEditorDir(msg.dir)
	if msg.err != nil {
		m.ShowNotification("The editor did not start or did not stop correctly: "+msg.err.Error(), "warning", m.Settings.NotificationDuration)
	}
	return func() tea.Msg { return hostTerminalBackMsg{} }
}

func (m *OS) dropScrollbackEditorDir(dir string) {
	_ = os.RemoveAll(dir)
	if m.scrollbackEditorDir == dir {
		m.scrollbackEditorDir = ""
	}
}

func (m *OS) removeScrollbackEditorFile() {
	if m.scrollbackEditorDir != "" {
		_ = os.RemoveAll(m.scrollbackEditorDir)
		m.scrollbackEditorDir = ""
	}
}

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

type scrollbackCaptureRequest struct {
	Session, Window string
	Host, Build     string
}

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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return 0, 0, errfmt.Errorf("read the capture: %w", err)
	}
	_, lines, err := writeScrollbackFile(path, func(out io.Writer) (int, error) {
		_, err := io.WriteString(out, vt.StripKittyPlaceholders(res.Content))
		return res.HistoryRows, err
	})
	return res.HistoryRows, lines, err
}

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

type lineCounter struct {
	newlines int
	open     bool
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
